package delivery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	channeltypes "csgclaw/internal/channel"
	feishustate "csgclaw/internal/channel/feishu/state"
	"csgclaw/internal/channel/feishu/transport"
)

type recordingAdapter struct {
	transport.Adapter
	mu        sync.Mutex
	texts     []transport.SendTextRequest
	updates   []transport.UpdateTextRequest
	textErr   error
	updateErr error
	messageID string
}

func (a *recordingAdapter) SendText(_ context.Context, req transport.SendTextRequest) (transport.SendResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.texts = append(a.texts, req)
	if a.textErr != nil {
		return transport.SendResult{}, a.textErr
	}
	return transport.SendResult{MessageID: a.messageID}, nil
}
func (a *recordingAdapter) UpdateText(_ context.Context, req transport.UpdateTextRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.updates = append(a.updates, req)
	return a.updateErr
}

func TestDispatcherDeliversFromMemory(t *testing.T) {
	store := feishustate.NewStore()
	intent := channeltypes.DeliveryIntent{
		ID: "text", BindingID: "binding-1", TurnID: "turn-1",
		Kind: channeltypes.DeliveryText, ChatID: "chat-1", Text: "answer",
	}
	if err := store.Enqueue(intent); err != nil {
		t.Fatal(err)
	}
	adapter := &recordingAdapter{messageID: "message-1"}
	dispatcher, err := NewDispatcher(DispatcherOptions{State: store, Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.drain(context.Background())
	delivered, ok := store.Delivery(intent.ID)
	if !ok || delivered.Status != channeltypes.DeliveryDelivered || delivered.MessageID != "message-1" {
		t.Fatalf("delivery = %#v, found=%t", delivered, ok)
	}
}

func TestDispatcherResolvesInMemoryUpdateDependency(t *testing.T) {
	store := feishustate.NewStore()
	create := channeltypes.DeliveryIntent{
		ID: "create", BindingID: "binding-1", TurnID: "turn-1",
		Kind: channeltypes.DeliveryMarkdown, ChatID: "chat-1", Text: "working",
	}
	update := channeltypes.DeliveryIntent{
		ID: "update", BindingID: "binding-1", TurnID: "turn-1",
		Kind: channeltypes.DeliveryMarkdownUpdate, RelatedID: create.ID, Text: "done",
	}
	if err := store.Enqueue(create); err != nil {
		t.Fatal(err)
	}
	if err := store.Enqueue(update); err != nil {
		t.Fatal(err)
	}
	adapter := &recordingAdapter{messageID: "message-1"}
	dispatcher, _ := NewDispatcher(DispatcherOptions{State: store, Adapter: adapter})
	dispatcher.drain(context.Background())
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.texts) != 1 || len(adapter.updates) != 1 || adapter.updates[0].MessageID != "message-1" {
		t.Fatalf("texts=%#v updates=%#v", adapter.texts, adapter.updates)
	}
}

func TestDispatcherUsesBoundedInProcessRetry(t *testing.T) {
	store := feishustate.NewStore()
	intent := channeltypes.DeliveryIntent{
		ID: "retry", BindingID: "binding-1", TurnID: "turn-1",
		Kind: channeltypes.DeliveryText, ChatID: "chat-1", Text: "answer",
	}
	if err := store.Enqueue(intent); err != nil {
		t.Fatal(err)
	}
	adapter := &recordingAdapter{textErr: &transport.APIError{Operation: "send", HTTPStatus: 503}}
	dispatcher, _ := NewDispatcher(DispatcherOptions{State: store, Adapter: adapter, RetryInterval: time.Nanosecond})
	for attempt := 0; attempt < maxDeliveryAttempts+1; attempt++ {
		dispatcher.drain(context.Background())
		time.Sleep(time.Millisecond)
	}
	failed, _ := store.Delivery(intent.ID)
	if failed.Status != channeltypes.DeliveryFailed || failed.Attempts != maxDeliveryAttempts {
		t.Fatalf("delivery = %#v", failed)
	}
}

func TestDispatcherDoesNotRetryPermanentFailure(t *testing.T) {
	store := feishustate.NewStore()
	intent := channeltypes.DeliveryIntent{
		ID: "permanent", BindingID: "binding-1", TurnID: "turn-1",
		Kind: channeltypes.DeliveryText, ChatID: "chat-1", Text: "answer",
	}
	if err := store.Enqueue(intent); err != nil {
		t.Fatal(err)
	}
	adapter := &recordingAdapter{textErr: &transport.APIError{Operation: "send", HTTPStatus: 400}}
	dispatcher, _ := NewDispatcher(DispatcherOptions{State: store, Adapter: adapter, RetryInterval: time.Nanosecond})
	dispatcher.drain(context.Background())
	dispatcher.drain(context.Background())
	failed, _ := store.Delivery(intent.ID)
	if failed.Status != channeltypes.DeliveryFailed || failed.Attempts != 1 {
		t.Fatalf("delivery = %#v", failed)
	}
	if got := len(adapter.texts); got != 1 {
		t.Fatalf("send attempts = %d, want 1", got)
	}
}

func TestRetryPolicyDoesNotRepeatAmbiguousUnsafeOperation(t *testing.T) {
	retryable := &transport.APIError{Operation: "send", HTTPStatus: 503}
	for _, kind := range []channeltypes.DeliveryKind{
		channeltypes.DeliveryReactionAdd,
		channeltypes.DeliveryCommentReply,
	} {
		if retryableDelivery(channeltypes.DeliveryIntent{Kind: kind}, retryable) {
			t.Fatalf("kind %q was unexpectedly retryable", kind)
		}
	}
	if !retryableDelivery(channeltypes.DeliveryIntent{Kind: channeltypes.DeliveryText}, retryable) {
		t.Fatal("stable-UUID message send was not retryable")
	}
}

func TestDispatcherUsesOnlyKnownEditLimitFallback(t *testing.T) {
	store := feishustate.NewStore()
	create := channeltypes.DeliveryIntent{
		ID: "turn-1:markdown:create", BindingID: "binding-1", TurnID: "turn-1",
		Kind: channeltypes.DeliveryMarkdown, ChatID: "chat-1", Text: "working",
	}
	if err := store.Enqueue(create); err != nil {
		t.Fatal(err)
	}
	create.MessageID = "message-1"
	if err := store.MarkDelivered(create); err != nil {
		t.Fatal(err)
	}
	terminal := channeltypes.DeliveryIntent{
		ID: "turn-1:markdown:final", BindingID: "binding-1", TurnID: "turn-1",
		Kind: channeltypes.DeliveryMarkdownUpdate, RelatedID: create.ID, Text: "done",
	}
	if err := store.Enqueue(terminal); err != nil {
		t.Fatal(err)
	}
	adapter := &recordingAdapter{
		messageID: "completion-1",
		updateErr: &transport.APIError{Operation: "update", Code: 230072},
	}
	dispatcher, _ := NewDispatcher(DispatcherOptions{State: store, Adapter: adapter})
	dispatcher.drain(context.Background())
	dispatcher.drain(context.Background())
	fallback, ok := store.Delivery(terminal.ID + ":completion")
	if !ok || fallback.Status != channeltypes.DeliveryDelivered || fallback.Text != "_（内容已结束）_" {
		t.Fatalf("fallback = %#v, found=%t", fallback, ok)
	}
	if got := len(adapter.updates); got != 1 {
		t.Fatalf("update attempts = %d, want 1", got)
	}
	if got := len(adapter.texts); got != 1 {
		t.Fatalf("completion sends = %d, want 1", got)
	}
}

func TestDispatcherPreventsOldStreamUpdateAfterTerminal(t *testing.T) {
	store := feishustate.NewStore()
	create := channeltypes.DeliveryIntent{
		ID: "turn-1:markdown:create", BindingID: "binding-1", TurnID: "turn-1",
		Kind: channeltypes.DeliveryMarkdown, ChatID: "chat-1", Text: "working",
	}
	if err := store.Enqueue(create); err != nil {
		t.Fatal(err)
	}
	create.MessageID = "message-1"
	if err := store.MarkDelivered(create); err != nil {
		t.Fatal(err)
	}
	old := channeltypes.DeliveryIntent{
		ID: "turn-1:markdown:update:1", BindingID: "binding-1", TurnID: "turn-1", Sequence: 1,
		Kind: channeltypes.DeliveryMarkdownUpdate, RelatedID: create.ID, Text: "old",
	}
	terminal := channeltypes.DeliveryIntent{
		ID: "turn-1:markdown:final", BindingID: "binding-1", TurnID: "turn-1", Sequence: 2,
		Kind: channeltypes.DeliveryMarkdownUpdate, RelatedID: create.ID, Text: "done",
	}
	if err := store.Enqueue(old); err != nil {
		t.Fatal(err)
	}
	if err := store.Enqueue(terminal); err != nil {
		t.Fatal(err)
	}
	adapter := &recordingAdapter{}
	dispatcher, _ := NewDispatcher(DispatcherOptions{State: store, Adapter: adapter})
	dispatcher.drain(context.Background())
	stale, _ := store.Delivery(old.ID)
	if stale.Status != channeltypes.DeliveryFailed {
		t.Fatalf("old update = %#v", stale)
	}
	if len(adapter.updates) != 1 || adapter.updates[0].Text != "done" {
		t.Fatalf("updates = %#v, want terminal only", adapter.updates)
	}
}

func TestDependencyErrorsRemainRecognizable(t *testing.T) {
	store := feishustate.NewStore()
	dispatcher, _ := NewDispatcher(DispatcherOptions{State: store, Adapter: &recordingAdapter{}})
	update := channeltypes.DeliveryIntent{
		ID: "update", BindingID: "binding-1", TurnID: "turn-1",
		Kind: channeltypes.DeliveryMarkdownUpdate, RelatedID: "missing", Text: "done",
	}
	if _, err := dispatcher.deliverMarkdownUpdate(context.Background(), update); !errors.Is(err, ErrDependencyTerminal) {
		t.Fatalf("dependency error = %v", err)
	}
}
