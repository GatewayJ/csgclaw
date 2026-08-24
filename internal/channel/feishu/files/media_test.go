package files

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"csgclaw/internal/agentengine"
	channeltypes "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/transport"
)

type fakeAdapter struct{ transport.Adapter }

func (fakeAdapter) DownloadResource(_ context.Context, req transport.DownloadResourceRequest) (transport.DownloadResourceResult, error) {
	content := []byte("attachment-content")
	if err := os.WriteFile(req.DestinationPath, content, 0o600); err != nil {
		return transport.DownloadResourceResult{}, err
	}
	return transport.DownloadResourceResult{ContentType: "text/plain", BytesWritten: int64(len(content))}, nil
}

type recordingDownloadAdapter struct {
	transport.Adapter
	limit int64
}

type canceledDownloadAdapter struct{ transport.Adapter }

func (canceledDownloadAdapter) DownloadResource(ctx context.Context, _ transport.DownloadResourceRequest) (transport.DownloadResourceResult, error) {
	return transport.DownloadResourceResult{}, ctx.Err()
}

func (a *recordingDownloadAdapter) DownloadResource(_ context.Context, req transport.DownloadResourceRequest) (transport.DownloadResourceResult, error) {
	a.limit = req.MaxBytes
	content := []byte("bounded")
	if err := os.WriteFile(req.DestinationPath, content, 0o600); err != nil {
		return transport.DownloadResourceResult{}, err
	}
	return transport.DownloadResourceResult{ContentType: "text/plain", BytesWritten: int64(len(content))}, nil
}

func testFileStore() agentengine.FileInterface {
	return agentengine.NewFileStore().Scope("agent-1")
}

func TestPreparerDownloadsHashesAndCleansAuthorizedFile(t *testing.T) {
	t.Parallel()
	files := testFileStore()
	preparer := &Preparer{Downloader: fakeAdapter{}, Files: files, Root: t.TempDir()}
	inputs, cleanup, err := preparer.Prepare(context.Background(), channeltypes.InboundMessage{
		TurnID: "turn-1",
		Source: channeltypes.Source{MessageID: "message-1"},
		Files:  []channeltypes.InboundFile{{Kind: "file", ID: "file-key", Name: "notes.txt", SizeBytes: int64(len("attachment-content"))}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].File == nil {
		t.Fatalf("inputs = %#v", inputs)
	}
	file := inputs[0].File
	if file.ID == "" {
		t.Fatalf("file = %#v", file)
	}
	download, err := files.Get(context.Background(), file.ID)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(download.Content)
	closeErr := download.Content.Close()
	if download.Metadata.Name != "notes.txt" || download.Metadata.MediaType != "text/plain" || len(download.Metadata.SHA256) != 64 ||
		readErr != nil || closeErr != nil || string(content) != "attachment-content" {
		t.Fatalf("download = %#v content=%q read=%v close=%v", download.Metadata, content, readErr, closeErr)
	}
	cleanup()
	if _, err := files.Get(context.Background(), file.ID); agentengine.ErrorCodeOf(err) != agentengine.ErrorFileNotFound {
		t.Fatalf("file after cleanup error = %v", err)
	}
}

func TestPreparerBoundsUnknownSizeAtTheTransport(t *testing.T) {
	t.Parallel()
	adapter := &recordingDownloadAdapter{}
	preparer := &Preparer{Downloader: adapter, Files: testFileStore(), Root: t.TempDir(), Policy: Policy{MaxFileBytes: 123}}
	_, cleanup, err := preparer.Prepare(context.Background(), channeltypes.InboundMessage{
		TurnID: "turn-1",
		Source: channeltypes.Source{MessageID: "message-1"},
		Files:  []channeltypes.InboundFile{{Kind: "file", ID: "file-key"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if adapter.limit != 123 {
		t.Fatalf("download limit = %d, want 123", adapter.limit)
	}
}

func TestCleanupStagingRemovesOnlyOwnedTemporaryFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	orphan := filepath.Join(root, stagingFilePrefix+"orphan")
	keep := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(orphan, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStaging(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan remains: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("unrelated file removed: %v", err)
	}
}

func TestPreparerRejectsOversizedDeclaredFileBeforeDownload(t *testing.T) {
	t.Parallel()
	preparer := &Preparer{Downloader: fakeAdapter{}, Files: testFileStore(), Root: t.TempDir(), Policy: Policy{MaxFileBytes: 1}}
	_, _, err := preparer.Prepare(context.Background(), channeltypes.InboundMessage{
		TurnID: "turn-1",
		Source: channeltypes.Source{MessageID: "message-1"},
		Files:  []channeltypes.InboundFile{{Kind: "file", ID: "file-key", SizeBytes: 2}},
	})
	if err == nil {
		t.Fatal("Prepare() error = nil")
	}
}

func TestPreparerPreservesCancellationInsteadOfReportingFileFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	preparer := &Preparer{Downloader: canceledDownloadAdapter{}, Files: testFileStore(), Root: t.TempDir()}
	_, _, err := preparer.Prepare(ctx, channeltypes.InboundMessage{
		TurnID: "turn-1",
		Source: channeltypes.Source{MessageID: "message-1"},
		Files:  []channeltypes.InboundFile{{Kind: "file", ID: "file-key"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Prepare() error = %v, want context cancellation", err)
	}
}
