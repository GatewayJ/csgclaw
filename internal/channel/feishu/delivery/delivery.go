package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	channeltypes "csgclaw/internal/channel"
	feishustate "csgclaw/internal/channel/feishu/state"
	"csgclaw/internal/channel/feishu/transport"
)

const (
	defaultRetryInterval = 2 * time.Second
	maxDeliveryAttempts  = 3
	// Feishu accepts at most twenty edits for a post message. Streaming must
	// leave one slot for the terminal snapshot, which removes the active footer.
	feishuMarkdownEditLimit = 20
)

var (
	ErrDeliverySuperseded             = errors.New("feishu delivery superseded by the terminal presentation")
	ErrPresentationEditBudgetReserved = errors.New("feishu presentation edit budget reserved for terminal update")
)

type DispatcherOptions struct {
	State         *feishustate.Store
	Adapter       transport.Adapter
	RetryInterval time.Duration
}

// Dispatcher drains process-local delivery intents outside Agent Engine event
// sinks. Retries are bounded and are not recovered after process restart.
type Dispatcher struct {
	state    *feishustate.Store
	adapter  transport.Adapter
	interval time.Duration
	wake     chan struct{}

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewDispatcher(options DispatcherOptions) (*Dispatcher, error) {
	if options.State == nil {
		return nil, fmt.Errorf("feishu delivery dispatcher: state is required")
	}
	if options.Adapter == nil {
		return nil, fmt.Errorf("feishu delivery dispatcher: transport adapter is required")
	}
	interval := options.RetryInterval
	if interval <= 0 {
		interval = defaultRetryInterval
	}
	return &Dispatcher{
		state:    options.State,
		adapter:  options.Adapter,
		interval: interval,
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
	}, nil
}

func (d *Dispatcher) Start(ctx context.Context) error {
	if d == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	d.mu.Lock()
	if d.cancel != nil {
		d.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.mu.Unlock()
	go d.run(runCtx)
	d.Notify()
	return nil
}

func (d *Dispatcher) Notify() {
	if d == nil {
		return
	}
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) Close() {
	if d == nil {
		return
	}
	d.mu.Lock()
	cancel := d.cancel
	if cancel != nil {
		cancel()
		d.cancel = nil
	}
	d.mu.Unlock()
	if cancel != nil {
		<-d.done
	}
}

func (d *Dispatcher) run(ctx context.Context) {
	defer close(d.done)
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.wake:
			d.drain(ctx)
		case <-ticker.C:
			d.drain(ctx)
		}
	}
}

func (d *Dispatcher) drain(ctx context.Context) {
	pending := d.state.Pending()
	superseded := d.supersededDeliveries(pending)
	blockedScopes := make(map[string]struct{})
	for _, intent := range pending {
		if ctx.Err() != nil {
			return
		}
		lane := deliveryLane(intent)
		if _, blocked := blockedScopes[lane]; blocked {
			continue
		}
		if _, stale := superseded[intent.ID]; stale {
			if err := d.state.MarkFailed(intent.ID, ErrDeliverySuperseded); err != nil {
				slog.Warn("record superseded Feishu presentation delivery failed", "intent_id", intent.ID, "error", err)
				blockedScopes[lane] = struct{}{}
			}
			continue
		}
		if !retryReady(intent, time.Now()) {
			blockedScopes[lane] = struct{}{}
			continue
		}
		if d.reserveTerminalMarkdownEdit(intent) {
			if err := d.state.MarkFailed(intent.ID, ErrPresentationEditBudgetReserved); err != nil {
				slog.Warn("reserve Feishu terminal presentation edit failed", "intent_id", intent.ID, "error", err)
				blockedScopes[lane] = struct{}{}
			}
			continue
		}
		if err := d.state.Begin(intent.ID); err != nil {
			slog.Warn("begin Feishu delivery failed", "intent_id", intent.ID, "error", err)
			blockedScopes[lane] = struct{}{}
			continue
		}
		delivered, err := d.deliver(ctx, intent)
		if err != nil {
			failedAt := time.Now()
			var markErr error
			retry := false
			if terminalMarkdownEditLimit(intent, err) {
				markErr = d.state.MarkFailed(intent.ID, err)
				if markErr == nil {
					if fallbackErr := d.enqueueTerminalCompletionFallback(intent); fallbackErr != nil {
						slog.Warn("enqueue Feishu terminal completion fallback failed", "intent_id", intent.ID, "error", fallbackErr)
					} else {
						slog.Warn("Feishu terminal presentation reached edit limit; queued completion fallback", "intent_id", intent.ID)
						d.Notify()
					}
				}
			} else if errors.Is(err, ErrDependencyTerminal) {
				markErr = d.state.MarkFailed(intent.ID, err)
			} else if errors.Is(err, ErrDependencyPending) || retryableDelivery(intent, err) {
				retry = true
				markErr = d.state.MarkRetryable(intent.ID, err, nextRetryAt(failedAt, d.interval, intent.Attempts+1))
			} else {
				// Permanent and unclassified failures are terminal.
				// Delivery failure never causes the Agent turn to run again.
				markErr = d.state.MarkFailed(intent.ID, err)
			}
			if markErr != nil {
				slog.Warn("record Feishu delivery failure failed", "intent_id", intent.ID, "error", markErr)
				blockedScopes[lane] = struct{}{}
			}
			slog.Warn("Feishu delivery failed", "intent_id", intent.ID, "kind", intent.Kind, "attempt", intent.Attempts+1, "error", err)
			if retry {
				blockedScopes[lane] = struct{}{}
			}
			continue
		}
		if err := d.state.MarkDelivered(delivered); err != nil {
			slog.Warn("record delivered Feishu intent failed", "intent_id", intent.ID, "error", err)
			blockedScopes[lane] = struct{}{}
			continue
		}
	}
}

func (d *Dispatcher) reserveTerminalMarkdownEdit(intent channeltypes.DeliveryIntent) bool {
	if d == nil || d.state == nil || intent.Kind != channeltypes.DeliveryMarkdownUpdate || terminalPresentationUpdate(intent) {
		return false
	}
	relatedID := strings.TrimSpace(intent.RelatedID)
	if relatedID == "" {
		return false
	}
	return d.state.DeliveredCount(channeltypes.DeliveryMarkdownUpdate, relatedID) >= feishuMarkdownEditLimit-1
}

func terminalPresentationUpdate(intent channeltypes.DeliveryIntent) bool {
	return presentationUpdate(intent.Kind) && strings.HasSuffix(strings.TrimSpace(intent.ID), ":final")
}

func terminalMarkdownEditLimit(intent channeltypes.DeliveryIntent, err error) bool {
	if intent.Kind != channeltypes.DeliveryMarkdownUpdate || !terminalPresentationUpdate(intent) || err == nil {
		return false
	}
	var apiErr *transport.APIError
	if errors.As(err, &apiErr) && apiErr.Code == 230072 {
		return true
	}
	return strings.Contains(err.Error(), "code=230072")
}

func terminalCompletionFallback(intent channeltypes.DeliveryIntent) channeltypes.DeliveryIntent {
	fallback := intent
	fallback.ID = strings.TrimSpace(intent.ID) + ":completion"
	fallback.Kind = channeltypes.DeliveryMarkdown
	fallback.RelatedID = ""
	fallback.MessageID = ""
	fallback.Card = nil
	fallback.Text = "_（内容已结束）_"
	fallback.Status = ""
	fallback.Attempts = 0
	fallback.LastError = ""
	fallback.NextAttemptAt = nil
	fallback.CreatedAt = time.Time{}
	return fallback
}

func (d *Dispatcher) enqueueTerminalCompletionFallback(intent channeltypes.DeliveryIntent) error {
	if d == nil || d.state == nil {
		return fmt.Errorf("Feishu delivery state is required")
	}
	return d.state.Enqueue(terminalCompletionFallback(intent))
}

func (d *Dispatcher) supersededDeliveries(pending []channeltypes.DeliveryIntent) map[string]struct{} {
	superseded := supersededPresentationUpdates(pending)
	for _, intent := range pending {
		// A terminal snapshot is the authoritative representation of a Turn. It
		// must also win over an older update that became pending after a retry.
		// The terminal update may already be delivered while an older streaming
		// update remains retryable.
		if d.supersededByTerminalPresentation(intent) {
			if superseded == nil {
				superseded = make(map[string]struct{})
			}
			superseded[intent.ID] = struct{}{}
		}
	}
	return superseded
}

func (d *Dispatcher) supersededByTerminalPresentation(intent channeltypes.DeliveryIntent) bool {
	if d == nil || d.state == nil || !presentationUpdate(intent.Kind) {
		return false
	}
	terminalID := presentationTerminalID(intent)
	if terminalID == "" {
		return false
	}
	terminal, ok := d.state.Intent(terminalID)
	return ok && terminal.ID != intent.ID && terminal.Kind == intent.Kind &&
		terminal.TurnID == intent.TurnID && terminal.BindingID == intent.BindingID
}

func presentationTerminalID(intent channeltypes.DeliveryIntent) string {
	if !presentationUpdate(intent.Kind) {
		return ""
	}
	relatedID := strings.TrimSpace(intent.RelatedID)
	if relatedID == "" || !strings.HasSuffix(relatedID, ":create") {
		return ""
	}
	return strings.TrimSuffix(relatedID, ":create") + ":final"
}

func supersededPresentationUpdates(pending []channeltypes.DeliveryIntent) map[string]struct{} {
	newest := make(map[string]channeltypes.DeliveryIntent)
	for _, intent := range pending {
		if !presentationUpdate(intent.Kind) {
			continue
		}
		lane := deliveryLane(intent)
		current, ok := newest[lane]
		if !ok || presentationUpdateAfter(intent, current) {
			newest[lane] = intent
		}
	}
	if len(newest) == 0 {
		return nil
	}
	superseded := make(map[string]struct{})
	for _, intent := range pending {
		if !presentationUpdate(intent.Kind) {
			continue
		}
		if latest := newest[deliveryLane(intent)]; latest.ID != intent.ID {
			superseded[intent.ID] = struct{}{}
		}
	}
	return superseded
}

func presentationUpdate(kind channeltypes.DeliveryKind) bool {
	return kind == channeltypes.DeliveryCardUpdate || kind == channeltypes.DeliveryMarkdownUpdate
}

func presentationUpdateAfter(candidate, current channeltypes.DeliveryIntent) bool {
	if candidate.Sequence != current.Sequence {
		return candidate.Sequence > current.Sequence
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.After(current.CreatedAt)
	}
	return candidate.ID > current.ID
}

func deliveryScope(intent channeltypes.DeliveryIntent) string {
	if intent.Kind == channeltypes.DeliveryCommentReply {
		return "comment:\x00" + intent.ResourceType + "\x00" + intent.ResourceID + "\x00" + intent.ParentID
	}
	if intent.ChatID == "" {
		return "message:\x00" + intent.MessageID
	}
	return intent.ChatID + "\x00" + intent.ThreadID + "\x00" + intent.ReplyTo
}

func deliveryLane(intent channeltypes.DeliveryIntent) string {
	scope := deliveryScope(intent)
	switch intent.Kind {
	case channeltypes.DeliveryCard:
		return scope + "\x00card\x00" + intent.TurnID + "\x00" + intent.ID
	case channeltypes.DeliveryCardUpdate:
		return scope + "\x00card\x00" + intent.TurnID + "\x00" + presentationMessageID(intent)
	case channeltypes.DeliveryMarkdown:
		return scope + "\x00markdown\x00" + intent.TurnID + "\x00" + intent.ID
	case channeltypes.DeliveryMarkdownUpdate:
		return scope + "\x00markdown\x00" + intent.TurnID + "\x00" + presentationMessageID(intent)
	case channeltypes.DeliveryReactionAdd, channeltypes.DeliveryReactionDelete:
		return scope + "\x00reaction\x00" + intent.TurnID
	default:
		return scope + "\x00message"
	}
}

func presentationMessageID(intent channeltypes.DeliveryIntent) string {
	if relatedID := strings.TrimSpace(intent.RelatedID); relatedID != "" {
		return relatedID
	}
	return intent.ID
}

func retryReady(intent channeltypes.DeliveryIntent, now time.Time) bool {
	if intent.NextAttemptAt == nil {
		return true
	}
	return !now.Before(*intent.NextAttemptAt)
}

func nextRetryAt(now time.Time, base time.Duration, attempt int) time.Time {
	if base <= 0 {
		base = defaultRetryInterval
	}
	shift := min(max(attempt-1, 0), 5)
	return now.Add(base * time.Duration(1<<shift))
}

func retryableDelivery(intent channeltypes.DeliveryIntent, err error) bool {
	attempt := intent.Attempts + 1
	if attempt >= maxDeliveryAttempts || !transport.IsRetryable(err) {
		return false
	}
	// Creates carry a stable Feishu UUID; updates and deletion target stable
	// remote IDs. Reaction creation and comment reply have no equivalent
	// deduplication key, so an ambiguous outcome must not be repeated.
	switch intent.Kind {
	case channeltypes.DeliveryText, channeltypes.DeliveryMarkdown, channeltypes.DeliveryCard,
		channeltypes.DeliveryMarkdownUpdate, channeltypes.DeliveryCardUpdate, channeltypes.DeliveryReactionDelete:
		return true
	default:
		return false
	}
}

func (d *Dispatcher) deliver(ctx context.Context, intent channeltypes.DeliveryIntent) (channeltypes.DeliveryIntent, error) {
	switch intent.Kind {
	case channeltypes.DeliveryText, channeltypes.DeliveryMarkdown:
		if err := d.messageDependency(intent); err != nil {
			return intent, err
		}
		return deliverText(ctx, d.adapter, intent)
	case channeltypes.DeliveryMarkdownUpdate:
		return d.deliverMarkdownUpdate(ctx, intent)
	case channeltypes.DeliveryCard:
		return deliverCard(ctx, d.adapter, intent)
	case channeltypes.DeliveryCardUpdate:
		return d.deliverCardUpdate(ctx, intent)
	case channeltypes.DeliveryReactionAdd:
		return deliverReactionAdd(ctx, d.adapter, intent)
	case channeltypes.DeliveryReactionDelete:
		return d.deliverReactionDelete(ctx, intent)
	case channeltypes.DeliveryCommentReply:
		comments, ok := d.adapter.(transport.CommentAdapter)
		if !ok {
			return intent, fmt.Errorf("Feishu comment delivery is unavailable")
		}
		return deliverCommentReply(ctx, comments, intent)
	default:
		return intent, fmt.Errorf("unsupported Feishu delivery kind %q", intent.Kind)
	}
}

func (d *Dispatcher) messageDependency(intent channeltypes.DeliveryIntent) error {
	relatedID := strings.TrimSpace(intent.RelatedID)
	if relatedID == "" {
		return nil
	}
	related, ok := d.state.Intent(relatedID)
	if !ok || related.Kind != intent.Kind || related.TurnID != intent.TurnID || related.BindingID != intent.BindingID {
		return fmt.Errorf("%w: previous message chunk %q is invalid", ErrDependencyTerminal, relatedID)
	}
	switch related.Status {
	case channeltypes.DeliveryDelivered:
		return nil
	case channeltypes.DeliveryPending, channeltypes.DeliveryDispatching:
		return fmt.Errorf("%w: previous message chunk %q is %s", ErrDependencyPending, relatedID, related.Status)
	case channeltypes.DeliveryFailed:
		return fmt.Errorf("%w: previous message chunk %q failed", ErrDependencyTerminal, relatedID)
	default:
		return fmt.Errorf("%w: previous message chunk %q has unsupported status %q", ErrDependencyTerminal, relatedID, related.Status)
	}
}

var _ interface{ Notify() } = (*Dispatcher)(nil)
