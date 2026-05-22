package codex

import (
	"context"
	"errors"
	"testing"
	"time"

	"csgclaw/internal/activity"
)

func TestPermissionActivityDeciderUsesConfiguredChannel(t *testing.T) {
	t.Parallel()

	events := NewEventSink()
	eventCh, cancel := events.Subscribe("rt-1")
	defer cancel()
	broker := NewPermissionBroker(events)

	resultCh := make(chan PermissionDecision, 1)
	go func() {
		decision, _ := broker.Request(context.Background(), PendingPermissionRequest{
			ExecutionRef: activity.ExecutionRef{
				RuntimeKind: "codex",
				RuntimeID:   "rt-1",
				SessionID:   "sess-1",
			},
			Options: []PermissionOptionSnapshot{
				{ID: "once", Kind: "allow_once", Label: "Allow once"},
			},
		})
		resultCh <- decision
	}()

	var requestID string
	select {
	case event := <-eventCh:
		requestID = event.ActionID
	case <-time.After(3 * time.Second):
		t.Fatal("permission request event was not published")
	}

	decider := NewPermissionActivityDecider("local-ui", broker)
	if _, err := decider.Decide(context.Background(), activity.ActivityDecisionRequest{
		Channel:    "csgclaw",
		ActivityID: requestID,
		OptionID:   "once",
	}); !errors.Is(err, activity.ErrActionNotFound) {
		t.Fatalf("mismatched channel Decide() error = %v, want action not found", err)
	}

	snapshot, err := decider.Decide(context.Background(), activity.ActivityDecisionRequest{
		Channel:    "local-ui",
		ActivityID: requestID,
		OptionID:   "once",
	})
	if err != nil {
		t.Fatalf("matching channel Decide() error = %v", err)
	}
	if snapshot.Status != activity.ActionStatusAllowed {
		t.Fatalf("snapshot = %+v, want allowed", snapshot)
	}

	select {
	case decision := <-resultCh:
		if decision.Snapshot.Status != PermissionStatusAllowed {
			t.Fatalf("decision status = %s, want allowed", decision.Snapshot.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("permission request did not finish")
	}
}

func TestNewPermissionActivityDeciderRejectsMissingInputs(t *testing.T) {
	t.Parallel()

	broker := NewPermissionBroker(nil)
	if got := NewPermissionActivityDecider("", broker); got != nil {
		t.Fatalf("NewPermissionActivityDecider(empty channel) = %#v, want nil", got)
	}
	if got := NewPermissionActivityDecider("local-ui", nil); got != nil {
		t.Fatalf("NewPermissionActivityDecider(nil permission) = %#v, want nil", got)
	}
}
