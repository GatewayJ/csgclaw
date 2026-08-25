package delivery

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"csgclaw/internal/agentengine"
	channeltypes "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/transport"
)

// FileResolver returns file content from the Agent scope owned by this binding.
type FileResolver interface {
	GetFile(context.Context, string) (agentengine.FileContent, error)
}

type fileResolverFunc func(context.Context, string) (agentengine.FileContent, error)

func (f fileResolverFunc) GetFile(ctx context.Context, fileID string) (agentengine.FileContent, error) {
	return f(ctx, fileID)
}

// NewEngineFileResolver adapts one binding's Agent-scoped Engine file API.
func NewEngineFileResolver(files agentengine.FileInterface) FileResolver {
	if files == nil {
		return nil
	}
	return engineFileResolver{files: files}
}

type engineFileResolver struct {
	files agentengine.FileInterface
}

func (r engineFileResolver) GetFile(ctx context.Context, fileID string) (agentengine.FileContent, error) {
	fileID = strings.TrimSpace(fileID)
	if r.files == nil || fileID == "" {
		return agentengine.FileContent{}, fmt.Errorf("Agent Engine file resolver requires a file ID")
	}
	return r.files.Get(ctx, fileID)
}

type mediaUpload struct {
	image bool
	key   string
}

func (d *Dispatcher) deliverMedia(ctx context.Context, intent channeltypes.DeliveryIntent) (channeltypes.DeliveryIntent, error) {
	if d == nil || d.files == nil {
		return intent, fmt.Errorf("Feishu file delivery is unavailable")
	}
	fileID := strings.TrimSpace(intent.FileID)
	if fileID == "" {
		return intent, fmt.Errorf("Feishu file delivery requires a file ID")
	}

	upload, ok := d.cachedMediaUpload(intent.ID)
	if !ok {
		var err error
		upload, err = d.uploadMedia(ctx, intent, fileID)
		if err != nil {
			return intent, err
		}
		d.cacheMediaUpload(intent.ID, upload)
	}

	result, err := d.sendMedia(ctx, intent, upload)
	if err != nil {
		return intent, err
	}
	intent.MessageID = result.MessageID
	return intent, nil
}

func (d *Dispatcher) uploadMedia(ctx context.Context, intent channeltypes.DeliveryIntent, fileID string) (mediaUpload, error) {
	download, err := d.files.GetFile(ctx, fileID)
	if err != nil {
		return mediaUpload{}, fmt.Errorf("load Agent Engine file %q for Feishu delivery: %w", fileID, err)
	}
	if download.Content == nil {
		return mediaUpload{}, fmt.Errorf("Agent Engine file %q content is unavailable", fileID)
	}

	metadata, metadataErr := authoritativeMediaMetadata(fileID, download.Metadata)
	if metadataErr != nil {
		_ = download.Content.Close()
		return mediaUpload{}, metadataErr
	}
	upload, uploadErr := d.uploadMediaContent(ctx, metadata, download.Content)
	closeErr := download.Content.Close()
	if uploadErr != nil {
		return mediaUpload{}, uploadErr
	}
	if closeErr != nil {
		slog.Warn("close uploaded Agent Engine file content failed",
			intentLogAttrs(intent, "file_id", fileID, "error", closeErr)...)
	}
	return upload, nil
}

func (d *Dispatcher) uploadMediaContent(ctx context.Context, metadata agentengine.OutputFileMetadata, content io.Reader) (mediaUpload, error) {
	if transport.SupportsImageUpload(metadata.MediaType, metadata.SizeBytes) {
		result, err := d.adapter.UploadImage(ctx, transport.UploadImageRequest{
			MediaType: metadata.MediaType,
			SizeBytes: metadata.SizeBytes,
			Content:   content,
		})
		if err != nil {
			return mediaUpload{}, err
		}
		if key := strings.TrimSpace(result.Key); key != "" {
			return mediaUpload{image: true, key: key}, nil
		}
		return mediaUpload{}, fmt.Errorf("Feishu image upload returned no image key")
	}

	result, err := d.adapter.UploadFile(ctx, transport.UploadFileRequest{
		Name:      metadata.Name,
		SizeBytes: metadata.SizeBytes,
		Content:   content,
	})
	if err != nil {
		return mediaUpload{}, err
	}
	if key := strings.TrimSpace(result.Key); key != "" {
		return mediaUpload{key: key}, nil
	}
	return mediaUpload{}, fmt.Errorf("Feishu file upload returned no file key")
}

func (d *Dispatcher) sendMedia(ctx context.Context, intent channeltypes.DeliveryIntent, upload mediaUpload) (transport.SendResult, error) {
	request := transport.SendFileRequest{
		ChatID:         intent.ChatID,
		FileKey:        upload.key,
		IdempotencyKey: intent.ID,
		ReplyTo:        intent.ReplyTo,
		ReplyInThread:  intent.ReplyTo != "" && intent.ThreadID != "",
		ThreadID:       intent.ThreadID,
	}
	if upload.image {
		return d.adapter.SendImage(ctx, transport.SendImageRequest{
			ChatID:         request.ChatID,
			ImageKey:       upload.key,
			IdempotencyKey: request.IdempotencyKey,
			ReplyTo:        request.ReplyTo,
			ReplyInThread:  request.ReplyInThread,
			ThreadID:       request.ThreadID,
		})
	}
	return d.adapter.SendFile(ctx, request)
}

func authoritativeMediaMetadata(fileID string, metadata agentengine.OutputFileMetadata) (agentengine.OutputFileMetadata, error) {
	metadata.ID = strings.TrimSpace(metadata.ID)
	metadata.Name = strings.TrimSpace(metadata.Name)
	metadata.MediaType = strings.TrimSpace(metadata.MediaType)
	if metadata.ID != fileID {
		return agentengine.OutputFileMetadata{}, fmt.Errorf("Agent Engine file ID %q did not match Feishu delivery file ID %q", metadata.ID, fileID)
	}
	if metadata.Name == "" || metadata.MediaType == "" || metadata.SizeBytes < 0 {
		return agentengine.OutputFileMetadata{}, fmt.Errorf("Agent Engine file %q returned invalid delivery metadata", fileID)
	}
	return metadata, nil
}

func (d *Dispatcher) cachedMediaUpload(intentID string) (mediaUpload, bool) {
	d.uploadMu.Lock()
	defer d.uploadMu.Unlock()
	upload, ok := d.uploads[strings.TrimSpace(intentID)]
	return upload, ok
}

func (d *Dispatcher) cacheMediaUpload(intentID string, upload mediaUpload) {
	d.uploadMu.Lock()
	defer d.uploadMu.Unlock()
	if d.uploads == nil {
		d.uploads = make(map[string]mediaUpload)
	}
	d.uploads[strings.TrimSpace(intentID)] = upload
}

func (d *Dispatcher) discardMediaUpload(intentID string) {
	if d == nil {
		return
	}
	d.uploadMu.Lock()
	delete(d.uploads, strings.TrimSpace(intentID))
	d.uploadMu.Unlock()
}
