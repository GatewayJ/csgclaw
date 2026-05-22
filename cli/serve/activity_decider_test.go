package serve

import (
	"context"
	"errors"
	"testing"
	"time"

	"csgclaw/internal/activity"
	runtimecodex "csgclaw/internal/runtime/codex"
)

func TestCodexPermissionActivityDeciderRequiresLocalChannel(t *testing.T) {
	t.Parallel()

	events := runtimecodex.NewEventSink()
	eventCh, cancel := events.Subscribe("rt-1")
	defer cancel()
	broker := runtimecodex.NewPermissionBroker(events)

	resultCh := make(chan runtimecodex.PermissionDecision, 1)
	go func() {
		decision, _ := broker.Request(context.Background(), runtimecodex.PendingPermissionRequest{
			ExecutionRef: activity.ExecutionRef{
				RuntimeKind: "codex",
				RuntimeID:   "rt-1",
				SessionID:   "sess-1",
			},
			Options: []runtimecodex.PermissionOptionSnapshot{
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

	decider := codexPermissionActivityDecider{permission: broker}
	if _, err := decider.Decide(context.Background(), activity.ActivityDecisionRequest{
		Channel:    "feishu",
		ActivityID: requestID,
		OptionID:   "once",
	}); !errors.Is(err, activity.ErrActionNotFound) {
		t.Fatalf("mismatched channel Decide() error = %v, want action not found", err)
	}

	snapshot, err := decider.Decide(context.Background(), activity.ActivityDecisionRequest{
		Channel:    "csgclaw",
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
		if decision.Snapshot.Status != runtimecodex.PermissionStatusAllowed {
			t.Fatalf("decision status = %s, want allowed", decision.Snapshot.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("permission request did not finish")
	}
}
