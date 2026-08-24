package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"csgclaw/internal/agentengine"
	channeltypes "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/transport"
)

const stagingFilePrefix = "feishu-attachment-"

// Preparer downloads Feishu resources into a private channel-owned staging
// directory, applies channel policy, and returns Engine-neutral file inputs.
type Preparer struct {
	Downloader transport.Adapter
	Files      agentengine.FileInterface
	Root       string
	Policy     Policy

	quotaOnce sync.Once
	quota     *bindingQuota
}

// Prepare returns inputs and a cleanup function. Cleanup is safe after
// AgentEngine.Run returns because the Engine copies authorized files into the
// selected Runtime workspace synchronously.
func (p *Preparer) Prepare(ctx context.Context, message channeltypes.InboundMessage) ([]agentengine.InputPart, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, func() {}, err
	}
	if len(message.Files) == 0 {
		return nil, func() {}, nil
	}
	if p == nil || p.Downloader == nil {
		return nil, func() {}, &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: "Feishu attachment download is unavailable"}
	}
	if p.Files == nil {
		return nil, func() {}, &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: "Feishu attachment file store is unavailable"}
	}
	if err := authorize(message.Files, p.Policy); err != nil {
		return nil, func() {}, &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: err.Error()}
	}
	slog.Debug("prepare Feishu attachments",
		"binding_id", message.Source.BindingID,
		"agent_id", message.AgentID,
		"turn_id", message.TurnID,
		"source_event_id", message.Source.EventID,
		"message_id", message.Source.MessageID,
		"chat_id", message.Source.ChatID,
		"file_count", len(message.Files),
	)
	root := strings.TrimSpace(p.Root)
	if root == "" {
		return nil, func() {}, &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: "Feishu attachment staging directory is unavailable"}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, func() {}, fileError("create Feishu attachment staging directory", err)
	}

	policy := p.Policy.normalized()
	reservedBytes, err := stagingReservation(message.Files, policy)
	if err != nil {
		return nil, func() {}, fileError("reserve Feishu attachment staging capacity", err)
	}
	p.quotaOnce.Do(func() {
		p.quota = newBindingQuota(policy)
	})
	reservation, err := p.quota.reserve(reservedBytes, len(message.Files))
	if err != nil {
		return nil, func() {}, fileError("reserve Feishu attachment staging capacity", err)
	}
	paths := make([]string, 0, len(message.Files))
	fileIDs := make([]string, 0, len(message.Files))
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			for _, path := range paths {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					slog.Warn("clean Feishu attachment staging file failed",
						attachmentMessageLogAttrs(message,
							"staging_path", path,
							"error", err,
						)...)
				}
			}
			for _, fileID := range fileIDs {
				if err := p.Files.Delete(context.Background(), fileID); err != nil &&
					agentengine.ErrorCodeOf(err) != agentengine.ErrorFileNotFound {
					slog.Warn("delete staged Feishu attachment from Agent Engine failed",
						attachmentMessageLogAttrs(message,
							"agent_file_id", fileID,
							"error", err,
						)...)
				}
			}
			reservation.release()
		})
	}
	inputs := make([]agentengine.InputPart, 0, len(message.Files))
	var actualTotal int64
	for _, resource := range message.Files {
		path, err := reservePath(root)
		if err != nil {
			cleanup()
			return nil, func() {}, fileError("reserve Feishu attachment path", err)
		}
		paths = append(paths, path)
		kind := transport.DownloadFile
		if strings.EqualFold(strings.TrimSpace(resource.Kind), "image") {
			kind = transport.DownloadImage
		}
		downloadLimit := min(policy.MaxFileBytes, policy.MaxTotal-actualTotal)
		if resource.SizeBytes > 0 {
			downloadLimit = min(downloadLimit, resource.SizeBytes)
		}
		if downloadLimit <= 0 {
			cleanup()
			return nil, func() {}, &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: "Feishu attachment download limit is exhausted"}
		}
		slog.Debug("download Feishu attachment",
			attachmentResourceLogAttrs(message, resource,
				"download_type", kind,
				"download_limit_bytes", downloadLimit,
				"staging_path", path,
			)...)
		releaseDownload, err := p.quota.acquireDownload(ctx)
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		result, err := func() (transport.DownloadResourceResult, error) {
			defer releaseDownload()
			return p.Downloader.DownloadResource(ctx, transport.DownloadResourceRequest{
				MessageID:       message.Source.MessageID,
				FileKey:         resource.ID,
				Type:            kind,
				DestinationPath: path,
				MaxBytes:        downloadLimit,
			})
		}()
		if err != nil {
			cleanup()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, func() {}, ctxErr
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, func() {}, err
			}
			slog.Warn("download Feishu attachment failed",
				attachmentResourceLogAttrs(message, resource,
					"download_type", kind,
					"download_limit_bytes", downloadLimit,
					"error", err,
				)...)
			return nil, func() {}, fileError("download Feishu attachment", err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			cleanup()
			slog.Warn("inspect Feishu attachment failed",
				attachmentResourceLogAttrs(message, resource,
					"staging_path", path,
					"error", err,
				)...)
			return nil, func() {}, fileError("inspect Feishu attachment", err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			cleanup()
			slog.Warn("reject downloaded Feishu attachment",
				attachmentResourceLogAttrs(message, resource,
					"staging_path", path,
					"size_bytes", info.Size(),
					"reason", "not_regular_file",
				)...)
			return nil, func() {}, &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: "downloaded Feishu attachment is not a regular file"}
		}
		if result.BytesWritten < 0 || (result.BytesWritten > 0 && result.BytesWritten != info.Size()) {
			cleanup()
			slog.Warn("reject downloaded Feishu attachment",
				attachmentResourceLogAttrs(message, resource,
					"staging_path", path,
					"transport_bytes_written", result.BytesWritten,
					"size_bytes", info.Size(),
					"reason", "transport_size_mismatch",
				)...)
			return nil, func() {}, &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: "downloaded Feishu attachment size does not match the transport result"}
		}
		if resource.SizeBytes > 0 && info.Size() > resource.SizeBytes {
			cleanup()
			slog.Warn("reject downloaded Feishu attachment",
				attachmentResourceLogAttrs(message, resource,
					"staging_path", path,
					"size_bytes", info.Size(),
					"reason", "declared_size_exceeded",
				)...)
			return nil, func() {}, &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: "downloaded Feishu attachment exceeds its declared size"}
		}
		if info.Size() > policy.MaxFileBytes {
			cleanup()
			slog.Warn("reject downloaded Feishu attachment",
				attachmentResourceLogAttrs(message, resource,
					"staging_path", path,
					"size_bytes", info.Size(),
					"max_file_bytes", policy.MaxFileBytes,
					"reason", "file_limit_exceeded",
				)...)
			return nil, func() {}, &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: fmt.Sprintf("Feishu attachment exceeds the %d byte limit", policy.MaxFileBytes)}
		}
		if info.Size() > policy.MaxTotal-actualTotal {
			cleanup()
			slog.Warn("reject downloaded Feishu attachment",
				attachmentResourceLogAttrs(message, resource,
					"staging_path", path,
					"size_bytes", info.Size(),
					"current_total_bytes", actualTotal,
					"max_total_bytes", policy.MaxTotal,
					"reason", "total_limit_exceeded",
				)...)
			return nil, func() {}, &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: fmt.Sprintf("Feishu attachments exceed the %d byte total limit", policy.MaxTotal)}
		}
		actualTotal += info.Size()
		hash, detectedType, err := inspectFile(path)
		if err != nil {
			cleanup()
			slog.Warn("authorize Feishu attachment failed",
				attachmentResourceLogAttrs(message, resource,
					"staging_path", path,
					"size_bytes", info.Size(),
					"error", err,
				)...)
			return nil, func() {}, fileError("authorize Feishu attachment", err)
		}
		mediaType := strings.TrimSpace(result.ContentType)
		if mediaType == "" {
			mediaType = detectedType
		}
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		source, err := os.Open(path)
		if err != nil {
			cleanup()
			slog.Warn("open authorized Feishu attachment failed",
				attachmentResourceLogAttrs(message, resource,
					"staging_path", path,
					"size_bytes", info.Size(),
					"mime_type", mediaType,
					"sha256", hash,
					"error", err,
				)...)
			return nil, func() {}, fileError("open authorized Feishu attachment", err)
		}
		created, createErr := p.Files.Create(ctx, agentengine.FileCreateRequest{
			Name:      safeName(resource.Name),
			MIMEType:  mediaType,
			SizeBytes: info.Size(),
			SHA256:    hash,
		}, source)
		closeErr := source.Close()
		if createErr != nil || closeErr != nil {
			cleanup()
			slog.Warn("store authorized Feishu attachment failed",
				attachmentResourceLogAttrs(message, resource,
					"staging_path", path,
					"size_bytes", info.Size(),
					"mime_type", mediaType,
					"sha256", hash,
					"error", errors.Join(createErr, closeErr),
				)...)
			return nil, func() {}, fileError("store authorized Feishu attachment", errors.Join(createErr, closeErr))
		}
		if strings.TrimSpace(created.ID) == "" {
			cleanup()
			slog.Warn("store authorized Feishu attachment returned empty file ID",
				attachmentResourceLogAttrs(message, resource,
					"staging_path", path,
					"size_bytes", info.Size(),
					"mime_type", mediaType,
					"sha256", hash,
				)...)
			return nil, func() {}, &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: "authorized Feishu attachment file ID is unavailable"}
		}
		fileIDs = append(fileIDs, created.ID)
		slog.Debug("stored Feishu attachment for Agent Engine",
			attachmentResourceLogAttrs(message, resource,
				"agent_file_id", created.ID,
				"size_bytes", info.Size(),
				"mime_type", mediaType,
				"sha256", hash,
			)...)
		inputs = append(inputs, agentengine.InputPart{
			Kind: agentengine.InputPartFile,
			File: &agentengine.InputFile{ID: created.ID},
		})
	}
	return inputs, cleanup, nil
}

func reservePath(root string) (string, error) {
	file, err := os.CreateTemp(root, stagingFilePrefix+"*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

// CleanupStaging removes files left by an interrupted process before a binding
// worker starts. Each binding owns a distinct hashed directory and Manager
// guarantees a single worker for that binding, so no live download can exist
// when this function is called.
func CleanupStaging(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("Feishu attachment staging directory is required")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), stagingFilePrefix) {
			continue
		}
		if err := os.Remove(filepath.Join(root, entry.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func inspectFile(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	h := sha256.New()
	var header [512]byte
	n, readErr := io.ReadFull(file, header[:])
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return "", "", readErr
	}
	if _, err := h.Write(header[:n]); err != nil {
		return "", "", err
	}
	if _, err := io.Copy(h, file); err != nil {
		return "", "", err
	}
	mediaType := mime.TypeByExtension(filepath.Ext(path))
	if mediaType == "" && n > 0 {
		mediaType = http.DetectContentType(header[:n])
	}
	return hex.EncodeToString(h.Sum(nil)), mediaType, nil
}

func fileError(operation string, err error) error {
	message := strings.TrimSpace(operation)
	if err != nil {
		message += ": " + err.Error()
	}
	return &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: message}
}

func attachmentMessageLogAttrs(message channeltypes.InboundMessage, extra ...any) []any {
	attrs := []any{
		"binding_id", message.Source.BindingID,
		"agent_id", message.AgentID,
		"turn_id", message.TurnID,
		"conversation_key", message.ConversationKey,
		"source_event_id", message.Source.EventID,
		"message_id", message.Source.MessageID,
		"chat_id", message.Source.ChatID,
		"thread_id", message.Source.ThreadID,
		"file_count", len(message.Files),
	}
	return append(attrs, extra...)
}

func attachmentResourceLogAttrs(message channeltypes.InboundMessage, resource channeltypes.InboundFile, extra ...any) []any {
	attrs := attachmentMessageLogAttrs(message,
		"file_key", resource.ID,
		"file_kind", resource.Kind,
		"file_name", resource.Name,
		"declared_size_bytes", resource.SizeBytes,
		"resource_url", resource.URL,
	)
	return append(attrs, extra...)
}
