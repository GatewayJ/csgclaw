package ingress

import (
	"context"
	"sync"
	"testing"
	"time"

	channeltypes "csgclaw/internal/channel"
	feishustate "csgclaw/internal/channel/feishu/state"
	"csgclaw/internal/channel/feishu/transport"
)

type testIntakeRunner struct {
	mu       sync.Mutex
	messages []channeltypes.InboundMessage
	started  chan string
	block    <-chan struct{}
}

func (r *testIntakeRunner) Submit(ctx context.Context, message channeltypes.InboundMessage) error {
	r.mu.Lock()
	r.messages = append(r.messages, message)
	r.mu.Unlock()
	if r.started != nil {
		r.started <- message.TurnID
	}
	if r.block != nil && message.Text == "first" {
		select {
		case <-r.block:
		case <-ctx.Done():
		}
	}
	return nil
}
func (r *testIntakeRunner) Reset(context.Context, channeltypes.InboundMessage) error { return nil }
func (r *testIntakeRunner) Cancel(context.Context, string, string, string) error     { return nil }
func (*testIntakeRunner) IsResetCommand(string) bool                                 { return false }
func (*testIntakeRunner) ActiveTurn(string) string                                   { return "" }

func TestIntakeDropsStartupBacklogAndLateEvents(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	runner := &testIntakeRunner{started: make(chan string, 2)}
	intake := newTestIntake(t, now, runner)

	for _, item := range []struct {
		id string
		at time.Time
	}{
		{id: "before-start", at: now.Add(-time.Minute)},
		{id: "too-late", at: now.Add(-3 * time.Minute)},
	} {
		if err := intake.HandleEvent(context.Background(), messageEvent(item.id, "chat-1", item.at, item.id)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case turn := <-runner.started:
		t.Fatalf("stale event reached runner: %s", turn)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestIntakeDropsOutOfOrderConversationEvent(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	runner := &testIntakeRunner{started: make(chan string, 2)}
	intake := newTestIntake(t, now, runner)

	newer := messageEvent("newer", "chat-1", now, "newer")
	older := messageEvent("older", "chat-1", now.Add(-time.Second), "older")
	if err := intake.HandleEvent(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	if err := intake.HandleEvent(context.Background(), older); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("fresh event did not reach runner")
	}
	select {
	case turn := <-runner.started:
		t.Fatalf("out-of-order event reached runner: %s", turn)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestIntakeDeduplicatesInMemory(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	runner := &testIntakeRunner{started: make(chan string, 2)}
	intake := newTestIntake(t, now, runner)
	event := messageEvent("same", "chat-1", now, "hello")
	if err := intake.HandleEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := intake.HandleEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("event did not reach runner")
	}
	select {
	case <-runner.started:
		t.Fatal("duplicate event reached runner")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestIntakeDropsNewEventWhenBufferIsFull(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	intake, err := NewIntake(IntakeOptions{
		Binding:   channeltypes.Binding{ID: "binding-1", Channel: "feishu", AgentID: "agent-1"},
		State:     feishustate.NewStore(),
		Runner:    &testIntakeRunner{},
		QueueSize: 1,
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	intake.SetIdentity(transport.Identity{OpenID: "bot"})
	if err := intake.HandleEvent(context.Background(), messageEvent("first", "chat-1", now, "first")); err != nil {
		t.Fatal(err)
	}
	if err := intake.HandleEvent(context.Background(), messageEvent("second", "chat-2", now, "second")); err != nil {
		t.Fatal(err)
	}
	if got := len(intake.queue); got != 1 {
		t.Fatalf("buffered events = %d, want 1", got)
	}
	item := <-intake.queue
	if item.message == nil || item.message.Text != "first" {
		t.Fatalf("buffered item = %#v, want first event", item)
	}
}

func TestIntakeBufferDoesNotSerializeConversationExecution(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	release := make(chan struct{})
	runner := &testIntakeRunner{started: make(chan string, 2), block: release}
	intake := newTestIntake(t, now, runner)
	if err := intake.HandleEvent(context.Background(), messageEvent("first", "chat-1", now, "first")); err != nil {
		t.Fatal(err)
	}
	if err := intake.HandleEvent(context.Background(), messageEvent("second", "chat-1", now.Add(time.Millisecond), "second")); err != nil {
		t.Fatal(err)
	}
	defer close(release)
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case turn := <-runner.started:
			seen[turn] = true
		case <-time.After(time.Second):
			t.Fatalf("runner starts = %v, want both events without waiting", seen)
		}
	}
}

func newTestIntake(t *testing.T, now time.Time, runner intakeRunner) *Intake {
	t.Helper()
	intake, err := NewIntake(IntakeOptions{
		Binding: channeltypes.Binding{ID: "binding-1", Channel: "feishu", AgentID: "agent-1"},
		State:   feishustate.NewStore(),
		Runner:  runner,
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	intake.SetIdentity(transport.Identity{OpenID: "bot"})
	if err := intake.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := intake.Activate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(intake.Close)
	return intake
}

func messageEvent(id, chatID string, createdAt time.Time, text string) transport.Event {
	return transport.Event{
		Kind:       transport.EventMessage,
		EventID:    id,
		OccurredAt: createdAt,
		Message: &transport.Message{
			ID: id, ChatID: chatID, ChatType: transport.ChatP2P,
			Text: text, Sender: transport.Identity{OpenID: "human"}, CreatedAt: createdAt,
		},
	}
}
