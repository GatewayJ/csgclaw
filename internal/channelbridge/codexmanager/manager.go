package codexmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"csgclaw/internal/agent"
	csgclawchannel "csgclaw/internal/channel/csgclaw"
	"csgclaw/internal/channelbridge/codexbridge"
	agentruntime "csgclaw/internal/runtime"
	runtimecodex "csgclaw/internal/runtime/codex"
	"csgclaw/internal/worklease"
)

type Manager interface {
	Start(context.Context) error
	EnsureAgent(context.Context, agent.Agent) error
	StopAgent(string)
	RefreshAgentChannel(context.Context, agent.Agent, string) error
	Close()
}

type AgentLister interface {
	List() []agent.Agent
}

type RuntimeProvider interface {
	Runtime(kind string) (agentruntime.Runtime, error)
}

type Options struct {
	Agents        AgentLister
	Runtimes      RuntimeProvider
	CSGClawClient codexbridge.BotClient
	WorkReporter  worklease.ParticipantWorkReporter
}

func New(opts Options) (Manager, error) {
	if opts.Agents == nil || opts.Runtimes == nil {
		return nil, nil
	}
	if opts.CSGClawClient == nil {
		return nil, nil
	}
	codexRuntime, events, err := resolveCodexRuntime(opts.Runtimes)
	if err != nil || codexRuntime == nil {
		return nil, err
	}

	return newCSGClawManager(managerDeps{
		agents:   opts.Agents,
		runtime:  codexRuntime,
		events:   events,
		client:   opts.CSGClawClient,
		reporter: opts.WorkReporter,
	}), nil
}

func resolveCodexRuntime(provider RuntimeProvider) (*runtimecodex.Runtime, *runtimecodex.EventSink, error) {
	if provider == nil {
		return nil, nil, nil
	}
	rt, err := provider.Runtime(agentruntime.KindCodex)
	if err != nil {
		return nil, nil, nil
	}
	codexRuntime, ok := rt.(*runtimecodex.Runtime)
	if !ok {
		return nil, nil, fmt.Errorf("runtime %q has unexpected type %T", agentruntime.KindCodex, rt)
	}
	events, ok := codexRuntime.EventSink().(*runtimecodex.EventSink)
	if !ok || events == nil {
		return nil, nil, fmt.Errorf("runtime %q is missing codex event sink", agentruntime.KindCodex)
	}
	return codexRuntime, events, nil
}

type managerDeps struct {
	agents   AgentLister
	runtime  *runtimecodex.Runtime
	events   *runtimecodex.EventSink
	client   codexbridge.BotClient
	reporter worklease.ParticipantWorkReporter
}

type csgclawManager struct {
	agents   AgentLister
	runtime  *runtimecodex.Runtime
	bridge   *codexbridge.Service
	ensuring ensureGate
}

func newCSGClawManager(deps managerDeps) *csgclawManager {
	bridgeOptions := []codexbridge.ServiceOption{
		codexbridge.WithUserInputBroker(deps.runtime.UserInputBroker()),
		codexbridge.WithParticipantWorkReporter(deps.reporter),
	}
	if registrar, ok := deps.reporter.(agentruntime.TurnControllerRegistrar); ok {
		bridgeOptions = append(bridgeOptions, codexbridge.WithTurnControllerRegistrar(registrar))
	}
	return &csgclawManager{
		agents:  deps.agents,
		runtime: deps.runtime,
		bridge: codexbridge.NewService(
			deps.client,
			deps.runtime.SessionManager(),
			deps.events,
			bridgeOptions...,
		),
		ensuring: newEnsureGate(),
	}
}

func (m *csgclawManager) Start(ctx context.Context) error {
	if m == nil || m.agents == nil || m.runtime == nil || m.bridge == nil {
		return nil
	}
	agents := m.agents.List()
	var startErr error
	for _, a := range agents {
		if !shouldRestoreCodexBridgeOnStartup(a) {
			continue
		}
		session, err := currentSession(m.runtime, a)
		if err != nil {
			startErr = errors.Join(startErr, fmt.Errorf("%s: %w", a.Name, err))
			continue
		}
		if err := m.bridge.StartBot(ctx, bindingForAgent(a, session.SessionID)); err != nil {
			startErr = errors.Join(startErr, fmt.Errorf("%s: %w", a.Name, err))
		}
	}
	return startErr
}

func (m *csgclawManager) EnsureAgent(ctx context.Context, a agent.Agent) error {
	if m == nil || m.runtime == nil || m.bridge == nil {
		return nil
	}
	if !shouldStartCodexBridge(a) {
		m.StopAgent(a.ID)
		return nil
	}
	if !m.ensuring.begin(a.ID) {
		return nil
	}
	for {
		session, err := currentSession(m.runtime, a)
		if err == nil {
			// Force a fresh bot-event subscription even when the binding is unchanged.
			// This repairs cases where the bridge worker exists but missed its initial
			// subscription window and would otherwise be treated as a no-op restart.
			m.stopAgentBridge(a)
			err = m.bridge.StartBot(ctx, bindingForAgent(a, session.SessionID))
		}
		if m.ensuring.finish(a.ID) {
			continue
		}
		return err
	}
}

func (m *csgclawManager) RefreshAgentChannel(ctx context.Context, a agent.Agent, channel string) error {
	channel = normalizeAgentChannel(channel)
	if !m.supportsAgentChannel(channel) {
		return unsupportedAgentChannelError(channel)
	}
	return m.EnsureAgent(ctx, a)
}

func (m *csgclawManager) supportsAgentChannel(channel string) bool {
	return normalizeAgentChannel(channel) == csgclawchannel.ChannelID
}

func (m *csgclawManager) StopAgent(agentID string) {
	if m == nil || m.bridge == nil {
		return
	}
	stopBotIDs(m.bridge, strings.TrimSpace(agentID), agent.ParticipantIDForAgent("", agentID))
}

func (m *csgclawManager) stopAgentBridge(a agent.Agent) {
	if m == nil || m.bridge == nil {
		return
	}
	stopBotIDs(m.bridge, strings.TrimSpace(a.ID), agent.ParticipantIDForAgent(a.Name, a.ID))
}

func (m *csgclawManager) Close() {
	if m == nil || m.bridge == nil {
		return
	}
	m.bridge.Close()
}

func (m *csgclawManager) PermissionDecider() runtimecodex.PermissionDecider {
	if m == nil || m.runtime == nil {
		return nil
	}
	return m.runtime.PermissionBroker()
}

func (m *csgclawManager) UserInputResponder() runtimecodex.UserInputBroker {
	if m == nil || m.runtime == nil {
		return nil
	}
	return m.runtime.UserInputBroker()
}

func (m *csgclawManager) SessionEventSource() *runtimecodex.Runtime {
	if m == nil {
		return nil
	}
	return m.runtime
}

func currentSession(runtime *runtimecodex.Runtime, a agent.Agent) (*runtimecodex.Session, error) {
	if runtime == nil {
		return nil, fmt.Errorf("codex runtime is required")
	}
	return runtime.SessionManager().LiveSession(runtimecodex.SessionHandle{RuntimeID: strings.TrimSpace(a.RuntimeID)})
}

func bindingForAgent(a agent.Agent, sessionID string) codexbridge.Binding {
	return codexbridge.Binding{
		BotID:     agent.ParticipantIDForAgent(a.Name, a.ID),
		RuntimeID: strings.TrimSpace(a.RuntimeID),
		SessionID: strings.TrimSpace(sessionID),
	}
}

func shouldStartCodexBridge(a agent.Agent) bool {
	if !isCodexBridgeRole(a.Role) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(a.RuntimeKind), agent.RuntimeKindCodex) {
		return false
	}
	if !(a.ProfileComplete || a.AgentProfile.ProfileComplete) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(a.Status), string(agentruntime.StateRunning))
}

func shouldRestoreCodexBridgeOnStartup(a agent.Agent) bool {
	if !isCodexBridgeRole(a.Role) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(a.RuntimeKind), agent.RuntimeKindCodex) {
		return false
	}
	if !(a.ProfileComplete || a.AgentProfile.ProfileComplete) {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(a.Status), string(agentruntime.StateStopped))
}

func isCodexBridgeRole(role string) bool {
	role = strings.TrimSpace(role)
	return strings.EqualFold(role, agent.RoleWorker) || strings.EqualFold(role, agent.RoleManager)
}

func normalizeAgentChannel(channel string) string {
	return strings.ToLower(strings.TrimSpace(channel))
}

func unsupportedAgentChannelError(channel string) error {
	channel = normalizeAgentChannel(channel)
	if channel == "" {
		return fmt.Errorf("channel is required")
	}
	return fmt.Errorf("channel %q is not supported by this codex bridge manager", channel)
}

type ensureGate struct {
	mu      sync.Mutex
	active  map[string]bool
	pending map[string]bool
}

func newEnsureGate() ensureGate {
	return ensureGate{active: make(map[string]bool), pending: make(map[string]bool)}
}

func (g *ensureGate) begin(agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active == nil {
		g.active = make(map[string]bool)
	}
	if g.active[agentID] {
		if g.pending == nil {
			g.pending = make(map[string]bool)
		}
		g.pending[agentID] = true
		return false
	}
	g.active[agentID] = true
	return true
}

func (g *ensureGate) finish(agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending[agentID] {
		delete(g.pending, agentID)
		return true
	}
	delete(g.active, agentID)
	return false
}

func stopBotIDs(bridge *codexbridge.Service, ids ...string) {
	if bridge == nil {
		return
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		bridge.StopBot(id)
	}
}
