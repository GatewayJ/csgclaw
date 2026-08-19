package ingress

import (
	"strings"
	"sync"

	channeltypes "csgclaw/internal/channel"
)

const defaultSeenWindow = 256

// Deduplicator keeps a bounded process-local window of recently accepted
// Feishu event IDs. It deliberately provides no restart recovery guarantee.
type Deduplicator struct {
	mu    sync.Mutex
	limit int
	seen  map[string]struct{}
	order []string
}

func NewDeduplicator(limit int) *Deduplicator {
	if limit <= 0 {
		limit = defaultSeenWindow
	}
	return &Deduplicator{limit: limit, seen: make(map[string]struct{}, limit)}
}

func (d *Deduplicator) Claim(source channeltypes.Source) bool {
	if d == nil {
		return true
	}
	logicalID := firstNonEmpty(source.DedupID, source.EventID)
	key := strings.TrimSpace(source.Channel) + "\x00" +
		strings.TrimSpace(source.BindingID) + "\x00" +
		logicalID
	if strings.Trim(key, "\x00") == "" {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.seen[key]; exists {
		return false
	}
	d.seen[key] = struct{}{}
	d.order = append(d.order, key)
	if len(d.order) > d.limit {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.seen, oldest)
	}
	return true
}
