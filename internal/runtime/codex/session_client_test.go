package codex

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

type recordingSink struct {
	mu     sync.Mutex
	events []SessionEvent
}

func (s *recordingSink) Publish(event SessionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingSink) snapshot() []SessionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionEvent, len(s.events))
	copy(out, s.events)
	return out
}

func waitForRuntime(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}

func TestSessionClientPublishesNormalizedEvents(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	client := &sessionClient{
		runtimeID: "rt-worker",
		eventSink: sink,
	}

	if err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.UpdateAgentMessageText("hello"),
	}); err != nil {
		t.Fatalf("SessionUpdate(agent message) error = %v", err)
	}

	if err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				ToolCallId: "tool-1",
				Title:      "Run shell command",
				Status:     acp.ToolCallStatusPending,
			},
		},
	}); err != nil {
		t.Fatalf("SessionUpdate(tool call) error = %v", err)
	}

	completed := acp.ToolCallStatusCompleted
	if err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{
				ToolCallId: "tool-1",
				Status:     &completed,
			},
		},
	}); err != nil {
		t.Fatalf("SessionUpdate(tool call update) error = %v", err)
	}

	events := sink.snapshot()
	if len(events) != 3 {
		t.Fatalf("published events = %d, want 3", len(events))
	}
	if events[0].Kind != SessionEventTextDelta || events[0].Text != "hello" {
		t.Fatalf("event[0] = %#v", events[0])
	}
	if events[1].Kind != SessionEventToolCallStart || events[1].ToolCallID != "tool-1" || events[1].ToolTitle != "Run shell command" {
		t.Fatalf("event[1] = %#v", events[1])
	}
	if events[2].Kind != SessionEventToolCallUpdate || events[2].ToolStatus != string(acp.ToolCallStatusCompleted) {
		t.Fatalf("event[2] = %#v", events[2])
	}
}

func TestSessionClientRequestPermissionWaitsForBrokerDecision(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	broker := NewPermissionBroker(sink)
	client := &sessionClient{
		runtimeID:        "rt-worker",
		eventSink:        sink,
		permissionBroker: broker,
	}

	respCh := make(chan acp.RequestPermissionResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := client.RequestPermission(context.Background(), acp.RequestPermissionRequest{
			SessionId: "sess-1",
			ToolCall:  acp.ToolCallUpdate{ToolCallId: "tool-1"},
			Options: []acp.PermissionOption{
				{OptionId: "always", Kind: acp.PermissionOptionKindAllowAlways, Name: "Always"},
				{OptionId: "once", Kind: acp.PermissionOptionKindAllowOnce, Name: "Once"},
			},
		})
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	waitForRuntime(t, func() bool {
		events := sink.snapshot()
		return len(events) == 1 && events[0].Kind == SessionEventPermissionRequest
	})
	events := sink.snapshot()
	_, err := broker.Decide(context.Background(), events[0].ActionID, "once")
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}

	var resp acp.RequestPermissionResponse
	select {
	case err := <-errCh:
		t.Fatalf("RequestPermission() error = %v", err)
	case resp = <-respCh:
	case <-time.After(3 * time.Second):
		t.Fatal("RequestPermission did not return after decision")
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "once" {
		t.Fatalf("selected option = %#v, want selected once", resp.Outcome.Selected)
	}

	events = sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("published events = %d, want 2", len(events))
	}
	if events[0].Kind != SessionEventPermissionRequest || events[0].ActionStatus != string(PermissionStatusPending) {
		t.Fatalf("event[0] = %#v", events[0])
	}
	if events[1].Kind != SessionEventPermissionDecision || events[1].ActionOptionID != "once" || events[1].ActionStatus != string(PermissionStatusAllowed) {
		t.Fatalf("event[1] = %#v", events[1])
	}
}

func TestSessionClientTerminalLifecycle(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	client := &sessionClient{
		workspaceDir: filepath.Join(root, "workspace"),
		homeDir:      filepath.Join(root, "home"),
		codexHomeDir: filepath.Join(root, "codex-home"),
		baseEnv:      os.Environ(),
		mkdirAll:     os.MkdirAll,
		readFile:     os.ReadFile,
		writeFile:    os.WriteFile,
		terminals:    make(map[string]*managedTerminal),
	}
	for _, dir := range []string{client.workspaceDir, client.homeDir, client.codexHomeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	resp, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command: "sh",
		Args:    []string{"-c", "printf 'hello from terminal'"},
	})
	if err != nil {
		t.Fatalf("CreateTerminal() error = %v", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exitResp, err := client.WaitForTerminalExit(waitCtx, acp.WaitForTerminalExitRequest{
		TerminalId: resp.TerminalId,
	})
	if err != nil {
		t.Fatalf("WaitForTerminalExit() error = %v", err)
	}
	if exitResp.ExitCode == nil || *exitResp.ExitCode != 0 {
		t.Fatalf("exit response = %#v, want exit code 0", exitResp)
	}

	outputResp, err := client.TerminalOutput(context.Background(), acp.TerminalOutputRequest{
		TerminalId: resp.TerminalId,
	})
	if err != nil {
		t.Fatalf("TerminalOutput() error = %v", err)
	}
	if outputResp.Output != "hello from terminal" {
		t.Fatalf("terminal output = %q", outputResp.Output)
	}
	if outputResp.ExitStatus == nil || outputResp.ExitStatus.ExitCode == nil || *outputResp.ExitStatus.ExitCode != 0 {
		t.Fatalf("terminal exit status = %#v", outputResp.ExitStatus)
	}
}
