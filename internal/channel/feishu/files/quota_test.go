package files

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"csgclaw/internal/agentengine"
	channeltypes "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/transport"
)

type prepareResult struct {
	cleanup func()
	err     error
}

func TestPolicyDefaultsBoundBindingResources(t *testing.T) {
	t.Parallel()
	policy := (Policy{}).normalized()
	if policy.MaxStagingBytes != 200<<20 || policy.MaxStagedFiles != 32 || policy.MaxConcurrentDownloads != 4 {
		t.Fatalf("binding resource defaults = (%d bytes, %d files, %d downloads)", policy.MaxStagingBytes, policy.MaxStagedFiles, policy.MaxConcurrentDownloads)
	}
}

type firstBlockingAdapter struct {
	transport.Adapter
	calls       atomic.Int32
	first       chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func newFirstBlockingAdapter() *firstBlockingAdapter {
	return &firstBlockingAdapter{
		first:   make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (a *firstBlockingAdapter) unblock() {
	a.releaseOnce.Do(func() { close(a.release) })
}

func (a *firstBlockingAdapter) DownloadResource(ctx context.Context, req transport.DownloadResourceRequest) (transport.DownloadResourceResult, error) {
	if a.calls.Add(1) == 1 {
		close(a.first)
		select {
		case <-a.release:
		case <-ctx.Done():
			return transport.DownloadResourceResult{}, ctx.Err()
		}
	}
	content := []byte("data")
	if err := os.WriteFile(req.DestinationPath, content, 0o600); err != nil {
		return transport.DownloadResourceResult{}, err
	}
	return transport.DownloadResourceResult{ContentType: "text/plain", BytesWritten: int64(len(content))}, nil
}

func TestPreparerRejectsConcurrentTurnAtBindingFileQuotaAndReleasesOnCleanup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	adapter := newFirstBlockingAdapter()
	t.Cleanup(adapter.unblock)
	preparer := &Preparer{
		Downloader: adapter,
		Files:      testFileStore(),
		Root:       root,
		Policy: Policy{
			MaxFileBytes:           4,
			MaxTotal:               4,
			MaxStagingBytes:        16,
			MaxStagedFiles:         1,
			MaxConcurrentDownloads: 2,
		},
	}

	firstResult := make(chan prepareResult, 1)
	go func() {
		_, cleanup, err := preparer.Prepare(context.Background(), attachmentMessage("turn-1", "file-1", 4))
		firstResult <- prepareResult{cleanup: cleanup, err: err}
	}()
	waitClosed(t, adapter.first, "first download did not start")

	_, _, err := preparer.Prepare(context.Background(), attachmentMessage("turn-2", "file-2", 4))
	assertFileUnavailable(t, err)
	if got := adapter.calls.Load(); got != 1 {
		t.Fatalf("download calls = %d, want 1 after quota rejection", got)
	}

	adapter.unblock()
	first := waitPrepareResult(t, firstResult)
	if first.err != nil {
		t.Fatalf("first Prepare() error = %v", first.err)
	}
	if first.cleanup == nil {
		t.Fatal("first Prepare() cleanup = nil")
	}
	first.cleanup()
	first.cleanup()

	_, cleanup, err := preparer.Prepare(context.Background(), attachmentMessage("turn-3", "file-3", 4))
	if err != nil {
		t.Fatalf("Prepare() after cleanup error = %v", err)
	}
	cleanup()
	cleanup()
	assertQuotaUsage(t, preparer, 0, 0)
}

type tinyDownloadAdapter struct {
	transport.Adapter
	calls atomic.Int32
}

func (a *tinyDownloadAdapter) DownloadResource(_ context.Context, req transport.DownloadResourceRequest) (transport.DownloadResourceResult, error) {
	a.calls.Add(1)
	if err := os.WriteFile(req.DestinationPath, []byte("x"), 0o600); err != nil {
		return transport.DownloadResourceResult{}, err
	}
	return transport.DownloadResourceResult{ContentType: "text/plain", BytesWritten: 1}, nil
}

func TestPreparerReservesMaxFileBytesForUnknownSizeUntilCleanup(t *testing.T) {
	t.Parallel()
	adapter := &tinyDownloadAdapter{}
	preparer := &Preparer{
		Downloader: adapter,
		Files:      testFileStore(),
		Root:       t.TempDir(),
		Policy: Policy{
			MaxFileBytes:    10,
			MaxTotal:        20,
			MaxStagingBytes: 15,
			MaxStagedFiles:  2,
		},
	}

	_, firstCleanup, err := preparer.Prepare(context.Background(), attachmentMessage("turn-1", "unknown", 0))
	if err != nil {
		t.Fatalf("unknown-size Prepare() error = %v", err)
	}
	assertQuotaUsage(t, preparer, 10, 1)

	_, _, err = preparer.Prepare(context.Background(), attachmentMessage("turn-2", "known", 6))
	assertFileUnavailable(t, err)
	if got := adapter.calls.Load(); got != 1 {
		t.Fatalf("download calls = %d, want 1 after byte quota rejection", got)
	}

	firstCleanup()
	firstCleanup()
	_, cleanup, err := preparer.Prepare(context.Background(), attachmentMessage("turn-3", "known", 6))
	if err != nil {
		t.Fatalf("Prepare() after unknown-size cleanup error = %v", err)
	}
	cleanup()
	assertQuotaUsage(t, preparer, 0, 0)
}

type failOnceDownloadAdapter struct {
	transport.Adapter
	calls atomic.Int32
}

func (a *failOnceDownloadAdapter) DownloadResource(_ context.Context, req transport.DownloadResourceRequest) (transport.DownloadResourceResult, error) {
	if a.calls.Add(1) == 1 {
		return transport.DownloadResourceResult{}, errors.New("injected download failure")
	}
	if err := os.WriteFile(req.DestinationPath, []byte("x"), 0o600); err != nil {
		return transport.DownloadResourceResult{}, err
	}
	return transport.DownloadResourceResult{ContentType: "text/plain", BytesWritten: 1}, nil
}

func TestPreparerReleasesBindingCapacityAndDownloadSlotOnError(t *testing.T) {
	t.Parallel()
	adapter := &failOnceDownloadAdapter{}
	preparer := &Preparer{
		Downloader: adapter,
		Files:      testFileStore(),
		Root:       t.TempDir(),
		Policy: Policy{
			MaxFileBytes:           1,
			MaxTotal:               1,
			MaxStagingBytes:        1,
			MaxStagedFiles:         1,
			MaxConcurrentDownloads: 1,
		},
	}

	_, _, err := preparer.Prepare(context.Background(), attachmentMessage("turn-1", "file-1", 1))
	assertFileUnavailable(t, err)
	assertQuotaUsage(t, preparer, 0, 0)

	_, cleanup, err := preparer.Prepare(context.Background(), attachmentMessage("turn-2", "file-2", 1))
	if err != nil {
		t.Fatalf("Prepare() after download error = %v", err)
	}
	cleanup()
	assertQuotaUsage(t, preparer, 0, 0)
}

type concurrencyAdapter struct {
	transport.Adapter
	active  atomic.Int32
	maximum atomic.Int32
	calls   atomic.Int32
	entered chan string
	release chan struct{}
	once    sync.Once
}

func newConcurrencyAdapter() *concurrencyAdapter {
	return &concurrencyAdapter{
		entered: make(chan string, 3),
		release: make(chan struct{}),
	}
}

func (a *concurrencyAdapter) unblock() {
	a.once.Do(func() { close(a.release) })
}

func (a *concurrencyAdapter) DownloadResource(ctx context.Context, req transport.DownloadResourceRequest) (transport.DownloadResourceResult, error) {
	a.calls.Add(1)
	active := a.active.Add(1)
	defer a.active.Add(-1)
	for {
		maximum := a.maximum.Load()
		if active <= maximum || a.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	a.entered <- req.FileKey
	select {
	case <-a.release:
	case <-ctx.Done():
		return transport.DownloadResourceResult{}, ctx.Err()
	}
	if err := os.WriteFile(req.DestinationPath, []byte("x"), 0o600); err != nil {
		return transport.DownloadResourceResult{}, err
	}
	return transport.DownloadResourceResult{ContentType: "text/plain", BytesWritten: 1}, nil
}

type observedAcquireContext struct {
	context.Context
	checks  atomic.Int32
	reached chan struct{}
	once    sync.Once
}

func (c *observedAcquireContext) Err() error {
	if c.checks.Add(1) == 2 {
		c.once.Do(func() { close(c.reached) })
	}
	return c.Context.Err()
}

func TestPreparerLimitsConcurrentDownloadsAndCancelsSlotWait(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	adapter := newConcurrencyAdapter()
	t.Cleanup(adapter.unblock)
	preparer := &Preparer{
		Downloader: adapter,
		Files:      testFileStore(),
		Root:       root,
		Policy: Policy{
			MaxFileBytes:           1,
			MaxTotal:               1,
			MaxStagingBytes:        10,
			MaxStagedFiles:         10,
			MaxConcurrentDownloads: 2,
		},
	}

	firstResult := make(chan prepareResult, 1)
	secondResult := make(chan prepareResult, 1)
	go prepareAsync(preparer, context.Background(), attachmentMessage("turn-1", "file-1", 1), firstResult)
	waitValue(t, adapter.entered, "first download did not enter")
	go prepareAsync(preparer, context.Background(), attachmentMessage("turn-2", "file-2", 1), secondResult)
	waitValue(t, adapter.entered, "second download did not enter")

	baseCtx, cancel := context.WithCancel(context.Background())
	thirdCtx := &observedAcquireContext{Context: baseCtx, reached: make(chan struct{})}
	thirdResult := make(chan prepareResult, 1)
	go prepareAsync(preparer, thirdCtx, attachmentMessage("turn-3", "file-3", 1), thirdResult)
	waitClosed(t, thirdCtx.reached, "third download did not reach the full slot queue")
	cancel()
	third := waitPrepareResult(t, thirdResult)
	if !errors.Is(third.err, context.Canceled) {
		t.Fatalf("third Prepare() error = %v, want context cancellation", third.err)
	}
	if got := adapter.calls.Load(); got != 2 {
		t.Fatalf("download calls = %d, want 2 while slots are full", got)
	}

	adapter.unblock()
	first := waitPrepareResult(t, firstResult)
	second := waitPrepareResult(t, secondResult)
	if first.err != nil || second.err != nil {
		t.Fatalf("active Prepare() errors = (%v, %v)", first.err, second.err)
	}
	first.cleanup()
	second.cleanup()
	if got := adapter.maximum.Load(); got != 2 {
		t.Fatalf("maximum concurrent downloads = %d, want 2", got)
	}
	assertQuotaUsage(t, preparer, 0, 0)
}

func prepareAsync(preparer *Preparer, ctx context.Context, message channeltypes.InboundMessage, result chan<- prepareResult) {
	_, cleanup, err := preparer.Prepare(ctx, message)
	result <- prepareResult{cleanup: cleanup, err: err}
}

func attachmentMessage(turnID, fileID string, size int64) channeltypes.InboundMessage {
	return channeltypes.InboundMessage{
		TurnID: turnID,
		Source: channeltypes.Source{MessageID: "message-" + turnID},
		Files:  []channeltypes.InboundFile{{Kind: "file", ID: fileID, Name: fileID + ".txt", SizeBytes: size}},
	}
}

func assertFileUnavailable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("Prepare() error = nil, want file_unavailable")
	}
	if code := agentengine.ErrorCodeOf(err); code != agentengine.ErrorFileUnavailable {
		t.Fatalf("Prepare() error code = %q, want %q (error: %v)", code, agentengine.ErrorFileUnavailable, err)
	}
}

func assertQuotaUsage(t *testing.T, preparer *Preparer, bytes int64, files int) {
	t.Helper()
	if preparer.quota == nil {
		t.Fatal("Preparer quota was not initialized")
	}
	preparer.quota.mu.Lock()
	defer preparer.quota.mu.Unlock()
	if preparer.quota.stagedBytes != bytes || preparer.quota.stagedFiles != files {
		t.Fatalf("quota usage = (%d bytes, %d files), want (%d bytes, %d files)", preparer.quota.stagedBytes, preparer.quota.stagedFiles, bytes, files)
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal(failure)
	}
}

func waitValue(t *testing.T, ch <-chan string, failure string) string {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal(failure)
		return ""
	}
}

func waitPrepareResult(t *testing.T, ch <-chan prepareResult) prepareResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("Prepare() did not return")
		return prepareResult{}
	}
}
