package codexmanager

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"csgclaw/internal/agent"
	"csgclaw/internal/channelbridge/codexbridge"
	agentruntime "csgclaw/internal/runtime"
	runtimecodex "csgclaw/internal/runtime/codex"
)

func TestShouldStartCodexBridge(t *testing.T) {
	cases := []struct {
		name  string
		agent agent.Agent
		want  bool
	}{
		{
			name: "running codex worker with complete profile",
			agent: agent.Agent{
				ID:              "u-alice",
				Role:            agent.RoleWorker,
				RuntimeKind:     agent.RuntimeKindCodex,
				Status:          string(agentruntime.StateRunning),
				ProfileComplete: true,
			},
			want: true,
		},
		{
			name: "stopped worker",
			agent: agent.Agent{
				ID:              "u-alice",
				Role:            agent.RoleWorker,
				RuntimeKind:     agent.RuntimeKindCodex,
				Status:          string(agentruntime.StateStopped),
				ProfileComplete: true,
			},
		},
		{
			name: "running codex manager with complete profile",
			agent: agent.Agent{
				ID:              agent.ManagerUserID,
				Role:            agent.RoleManager,
				RuntimeKind:     agent.RuntimeKindCodex,
				Status:          string(agentruntime.StateRunning),
				ProfileComplete: true,
			},
			want: true,
		},
		{
			name: "non-codex worker is excluded",
			agent: agent.Agent{
				ID:              "u-alice",
				Role:            agent.RoleWorker,
				RuntimeKind:     agent.RuntimeKindPicoClawSandbox,
				Status:          string(agentruntime.StateRunning),
				ProfileComplete: true,
			},
		},
		{
			name: "incomplete profile is excluded",
			agent: agent.Agent{
				ID:          "u-alice",
				Role:        agent.RoleWorker,
				RuntimeKind: agent.RuntimeKindCodex,
				Status:      string(agentruntime.StateRunning),
			},
		},
	}

	for _, tc := range cases {
		if got := shouldStartCodexBridge(tc.agent); got != tc.want {
			t.Fatalf("%s: shouldStartCodexBridge() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEnsureGateCoalescesOverlappingEnsures(t *testing.T) {
	gate := newEnsureGate()
	if !gate.begin("u-manager") {
		t.Fatal("first begin() = false, want true")
	}
	if gate.begin("u-manager") {
		t.Fatal("overlapping begin() = true, want false")
	}
	if !gate.finish("u-manager") {
		t.Fatal("first finish() = false, want a coalesced rerun")
	}
	if gate.finish("u-manager") {
		t.Fatal("second finish() = true, want no further rerun")
	}
	if !gate.begin("u-manager") {
		t.Fatal("begin() after finish = false, want true")
	}
	if gate.finish("u-manager") {
		t.Fatal("finish() without overlap = true, want false")
	}
}

func TestShouldRestoreCodexBridgeOnStartup(t *testing.T) {
	cases := []struct {
		name  string
		agent agent.Agent
		want  bool
	}{
		{
			name: "running codex worker is restored",
			agent: agent.Agent{
				ID:              "u-alice",
				Role:            agent.RoleWorker,
				RuntimeKind:     agent.RuntimeKindCodex,
				Status:          string(agentruntime.StateRunning),
				ProfileComplete: true,
			},
			want: true,
		},
		{
			name: "exited codex worker is restored",
			agent: agent.Agent{
				ID:              "u-alice",
				Role:            agent.RoleWorker,
				RuntimeKind:     agent.RuntimeKindCodex,
				Status:          string(agentruntime.StateExited),
				ProfileComplete: true,
			},
			want: true,
		},
		{
			name: "stopped codex worker stays stopped",
			agent: agent.Agent{
				ID:              "u-alice",
				Role:            agent.RoleWorker,
				RuntimeKind:     agent.RuntimeKindCodex,
				Status:          string(agentruntime.StateStopped),
				ProfileComplete: true,
			},
		},
		{
			name: "running codex manager is restored",
			agent: agent.Agent{
				ID:              agent.ManagerUserID,
				Role:            agent.RoleManager,
				RuntimeKind:     agent.RuntimeKindCodex,
				Status:          string(agentruntime.StateRunning),
				ProfileComplete: true,
			},
			want: true,
		},
	}

	for _, tc := range cases {
		if got := shouldRestoreCodexBridgeOnStartup(tc.agent); got != tc.want {
			t.Fatalf("%s: shouldRestoreCodexBridgeOnStartup() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBindingForAgentUsesManagerParticipantID(t *testing.T) {
	binding := bindingForAgent(agent.Agent{
		ID:          agent.ManagerUserID,
		Name:        agent.ManagerName,
		RuntimeKind: agent.RuntimeKindCodex,
		RuntimeID:   "rt-agent-manager",
		Role:        agent.RoleManager,
	}, "sess-manager")

	if binding.BotID != agent.ManagerParticipantID {
		t.Fatalf("BotID = %q, want %q", binding.BotID, agent.ManagerParticipantID)
	}
	if binding.RuntimeID != "rt-agent-manager" || binding.SessionID != "sess-manager" {
		t.Fatalf("binding runtime/session = %q/%q, want rt-agent-manager/sess-manager", binding.RuntimeID, binding.SessionID)
	}
}

func TestBindingForAgentUsesParticipantIDForWorker(t *testing.T) {
	binding := bindingForAgent(agent.Agent{
		ID:          "u-agent-3l6htd",
		Name:        "dev",
		RuntimeKind: agent.RuntimeKindCodex,
		RuntimeID:   "rt-u-agent-3l6htd",
	}, "sess-dev")

	if binding.BotID != "pt-3l6htd" {
		t.Fatalf("BotID = %q, want participant ID pt-3l6htd", binding.BotID)
	}
	if binding.RuntimeID != "rt-u-agent-3l6htd" || binding.SessionID != "sess-dev" {
		t.Fatalf("binding = %+v, want runtime/session preserved", binding)
	}
}

func TestCSGClawManagerStartupDoesNotRestartMissingSession(t *testing.T) {
	runtime := runtimecodex.New(runtimecodex.Dependencies{Manager: missingSessionManager{}})
	manager := newCSGClawManager(managerDeps{
		agents: staticAgentLister{agents: []agent.Agent{{
			ID:              agent.ManagerUserID,
			Name:            agent.ManagerName,
			Role:            agent.RoleManager,
			RuntimeKind:     agent.RuntimeKindCodex,
			RuntimeID:       "rt-agent-manager",
			Status:          string(agentruntime.StateRunning),
			ProfileComplete: true,
		}}},
		runtime: runtime,
		client:  newRecordingBotClient(),
	})

	err := manager.Start(context.Background())
	if err == nil {
		t.Fatal("Start() error = nil, want missing session error")
	}
}

func TestCSGClawManagerStartupOnlyAttachesToLiveSession(t *testing.T) {
	sessions := &trackingSessionManager{
		live: &runtimecodex.Session{
			RuntimeID: "rt-agent-manager",
			SessionID: "sess-manager",
		},
	}
	runtime := runtimecodex.New(runtimecodex.Dependencies{Manager: sessions})
	manager := newCSGClawManager(managerDeps{
		agents: staticAgentLister{agents: []agent.Agent{{
			ID:              agent.ManagerUserID,
			Name:            agent.ManagerName,
			Role:            agent.RoleManager,
			RuntimeKind:     agent.RuntimeKindCodex,
			RuntimeID:       "rt-agent-manager",
			Status:          string(agentruntime.StateRunning),
			ProfileComplete: true,
		}}},
		runtime: runtime,
		client:  newRecordingBotClient(),
	})

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if sessions.liveCalls != 1 {
		t.Fatalf("LiveSession() calls = %d, want 1", sessions.liveCalls)
	}
	if sessions.sessionCalls != 0 {
		t.Fatalf("Session() calls = %d, want 0 because bridge startup must not hydrate", sessions.sessionCalls)
	}
}

type staticAgentLister struct {
	agents []agent.Agent
}

func (l staticAgentLister) List() []agent.Agent {
	return append([]agent.Agent(nil), l.agents...)
}

type missingSessionManager struct{}

func (missingSessionManager) Start(context.Context, runtimecodex.SessionSpec) (*runtimecodex.Session, error) {
	return nil, os.ErrNotExist
}

func (missingSessionManager) Stop(context.Context, runtimecodex.SessionHandle) error {
	return os.ErrNotExist
}

func (missingSessionManager) LiveSession(runtimecodex.SessionHandle) (*runtimecodex.Session, error) {
	return nil, os.ErrNotExist
}

func (missingSessionManager) Session(runtimecodex.SessionHandle) (*runtimecodex.Session, error) {
	return nil, os.ErrNotExist
}

func (missingSessionManager) Prompt(context.Context, runtimecodex.SessionHandle, runtimecodex.PromptRequest) (runtimecodex.PromptResponse, error) {
	return runtimecodex.PromptResponse{}, os.ErrNotExist
}

type trackingSessionManager struct {
	live         *runtimecodex.Session
	liveCalls    int
	sessionCalls int
}

func (m *trackingSessionManager) Start(context.Context, runtimecodex.SessionSpec) (*runtimecodex.Session, error) {
	return nil, os.ErrNotExist
}

func (m *trackingSessionManager) Stop(context.Context, runtimecodex.SessionHandle) error {
	return nil
}

func (m *trackingSessionManager) LiveSession(runtimecodex.SessionHandle) (*runtimecodex.Session, error) {
	m.liveCalls++
	if m.live == nil {
		return nil, os.ErrNotExist
	}
	cloned := *m.live
	return &cloned, nil
}

func (m *trackingSessionManager) Session(runtimecodex.SessionHandle) (*runtimecodex.Session, error) {
	m.sessionCalls++
	return nil, os.ErrNotExist
}

func (m *trackingSessionManager) Prompt(context.Context, runtimecodex.SessionHandle, runtimecodex.PromptRequest) (runtimecodex.PromptResponse, error) {
	return runtimecodex.PromptResponse{}, os.ErrNotExist
}

type recordingBotClient struct {
	mu      sync.Mutex
	started map[string]chan struct{}
	stopped map[string]chan struct{}
}

func newRecordingBotClient() *recordingBotClient {
	return &recordingBotClient{
		started: make(map[string]chan struct{}),
		stopped: make(map[string]chan struct{}),
	}
}

func (c *recordingBotClient) StreamEvents(ctx context.Context, botID string, _ string) (<-chan codexbridge.BotEvent, <-chan error) {
	events := make(chan codexbridge.BotEvent)
	errs := make(chan error)
	c.signal(c.started, botID)
	go func() {
		defer close(events)
		defer close(errs)
		<-ctx.Done()
		c.signal(c.stopped, botID)
	}()
	return events, errs
}

func (c *recordingBotClient) SendMessage(context.Context, string, codexbridge.SendMessageRequest) (codexbridge.SendMessageResponse, error) {
	return codexbridge.SendMessageResponse{MessageID: "msg-1"}, nil
}

func (c *recordingBotClient) waitStarted(t *testing.T, botID string) {
	t.Helper()
	c.wait(t, c.started, botID, "start")
}

func (c *recordingBotClient) waitStopped(t *testing.T, botID string) {
	t.Helper()
	c.wait(t, c.stopped, botID, "stop")
}

func (c *recordingBotClient) totalStartedSignals() int {
	return c.totalSignals(c.started)
}

func (c *recordingBotClient) totalStoppedSignals() int {
	return c.totalSignals(c.stopped)
}

func (c *recordingBotClient) totalSignals(signals map[string]chan struct{}) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, ch := range signals {
		total += len(ch)
	}
	return total
}

func (c *recordingBotClient) wait(t *testing.T, signals map[string]chan struct{}, botID string, action string) {
	t.Helper()
	ch := c.signalChannel(signals, botID)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for bot %s to %s", botID, action)
	}
}

func (c *recordingBotClient) signal(signals map[string]chan struct{}, botID string) {
	ch := c.signalChannel(signals, botID)
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (c *recordingBotClient) signalChannel(signals map[string]chan struct{}, botID string) chan struct{} {
	botID = strings.TrimSpace(botID)
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := signals[botID]
	if ch == nil {
		ch = make(chan struct{}, 8)
		signals[botID] = ch
	}
	return ch
}
