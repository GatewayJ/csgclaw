package files

import (
	"context"
	"errors"
	"sync"

	channeltypes "csgclaw/internal/channel"
)

var (
	errStagingByteQuota = errors.New("Feishu attachment staging byte quota is exhausted")
	errStagingFileQuota = errors.New("Feishu attachment staging file quota is exhausted")
)

// bindingQuota bounds resources retained by one binding across concurrent
// turns. It is intentionally owned by a Preparer rather than shared globally.
type bindingQuota struct {
	mu          sync.Mutex
	maxBytes    int64
	maxFiles    int
	stagedBytes int64
	stagedFiles int
	downloads   chan struct{}
}

func newBindingQuota(policy Policy) *bindingQuota {
	policy = policy.normalized()
	return &bindingQuota{
		maxBytes:  policy.MaxStagingBytes,
		maxFiles:  policy.MaxStagedFiles,
		downloads: make(chan struct{}, policy.MaxConcurrentDownloads),
	}
}

func stagingReservation(resources []channeltypes.InboundFile, policy Policy) (int64, error) {
	policy = policy.normalized()
	var total int64
	for _, resource := range resources {
		size := resource.SizeBytes
		if size == 0 {
			size = policy.MaxFileBytes
		}
		if size > policy.MaxStagingBytes-total {
			return 0, errStagingByteQuota
		}
		total += size
	}
	return total, nil
}

func (q *bindingQuota) reserve(bytes int64, files int) (*quotaReservation, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if files > q.maxFiles-q.stagedFiles {
		return nil, errStagingFileQuota
	}
	if bytes > q.maxBytes-q.stagedBytes {
		return nil, errStagingByteQuota
	}
	q.stagedBytes += bytes
	q.stagedFiles += files
	return &quotaReservation{quota: q, bytes: bytes, files: files}, nil
}

func (q *bindingQuota) acquireDownload(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case q.downloads <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-q.downloads
			return nil, err
		}
		var once sync.Once
		return func() {
			once.Do(func() { <-q.downloads })
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type quotaReservation struct {
	quota *bindingQuota
	bytes int64
	files int
	once  sync.Once
}

func (r *quotaReservation) release() {
	if r == nil || r.quota == nil {
		return
	}
	r.once.Do(func() {
		r.quota.mu.Lock()
		r.quota.stagedBytes -= r.bytes
		r.quota.stagedFiles -= r.files
		r.quota.mu.Unlock()
	})
}
