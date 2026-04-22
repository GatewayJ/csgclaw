//go:build csghub
// +build csghub

package agent

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"csgclaw/internal/config"
	"csgclaw/internal/sandbox"
)

// --- Service ----------------------------------------------------------------

// Service owns the persisted agent registry and the CSGHub Sandbox
// lifecycle calls used to back it. The struct intentionally mirrors the
// public surface of the BoxLite Service (methods, field semantics) so
// upstream callers (cli/, internal/api/, internal/bot/) do not need to
// branch on build tags.
//
// Lifecycle is delegated to a sandbox.Provider injected by
// cli/sandboxopts so WithSandboxProvider is honored symmetrically with
// the BoxLite build.
type Service struct {
	llm          config.LLMConfig
	server       config.ServerConfig
	channels     config.ChannelsConfig
	managerImage string
	downstream   map[string]string
	state        string
	mu           sync.RWMutex
	provider     sandbox.Provider
	runtime      sandbox.Runtime
	agents       map[string]Agent
}

var defaultSandboxProvider sandbox.Provider = unconfiguredSandboxProvider{}

type unconfiguredSandboxProvider struct{}

func (unconfiguredSandboxProvider) Name() string { return "unconfigured" }

func (unconfiguredSandboxProvider) Open(context.Context, string) (sandbox.Runtime, error) {
	return nil, fmt.Errorf("sandbox provider is not configured")
}

type streamExecuteRuntime interface {
	sandbox.Runtime
	StreamExecute(ctx context.Context, name, command string, emit func(line string) error) error
}

type providerAgentDefaults interface {
	SandboxDownstreamEnv() map[string]string
	SandboxManagerImage() string
}

type providerAgentNamer interface {
	AgentSandboxName(agentID string) string
}

type providerAgentMountPaths interface {
	AgentMountHostPaths(agentName string) (picoClaw, workspace, projects string, err error)
}

type gatewayHostPaths struct {
	PicoClaw  string
	Workspace string
	Projects  string
}

func (s *Service) createWorkerBackend(ctx context.Context, name, id string, model config.ModelConfig) (sandbox.Info, string, error) {
	rt, err := s.runtimeOrOpen()
	if err != nil {
		return sandbox.Info{}, "", err
	}
	paths, mounts, err := s.gatewayMounts(name)
	if err != nil {
		return sandbox.Info{}, "", err
	}
	if _, err := ensureAgentPicoClawConfigAt(paths.PicoClaw, id, s.server, model); err != nil {
		return sandbox.Info{}, "", err
	}
	if _, err := ensureAgentWorkspaceAt(paths.Workspace, workspaceTemplateForAgent(name, id)); err != nil {
		return sandbox.Info{}, "", err
	}
	env := agentSandboxEnv(s.downstream, s.server, s.channels, id, model.ModelID)
	spec := buildGatewaySpec(s.managerImage, s.sandboxName(id), env, mounts)

	info, err := s.ensureAgentSandbox(ctx, rt, spec, false)
	if err != nil {
		return sandbox.Info{}, "", fmt.Errorf("create worker sandbox: %w", err)
	}
	return info, sandboxNameFromInfo(info, spec.Name), nil
}

func (s *Service) deleteSandboxBackend(ctx context.Context, agent Agent) error {
	rt, err := s.runtimeOrOpen()
	if err != nil {
		return err
	}
	name := strings.TrimSpace(agent.BoxID)
	if name == "" {
		name = s.sandboxName(agent.ID)
	}
	if err := rt.Remove(ctx, name, sandbox.RemoveOptions{}); err != nil && !sandbox.IsNotFound(err) {
		return fmt.Errorf("stop sandbox %q: %w", name, err)
	}
	return nil
}

// postDeleteLocked is a no-op on CSGHub: a single shared runtime is
// owned by the Service, so there is no per-agent bookkeeping to prune
// when an agent is removed.
func (s *Service) postDeleteLockedBackend(Agent) {}

func (s *Service) agentHomeDirBackend(agentName string) (string, error) {
	paths, err := s.agentMountHostPaths(agentName)
	if err != nil {
		return "", err
	}
	return filepath.Dir(paths.PicoClaw), nil
}

// ensureManager materializes the manager's picoclaw config + tenant
// workspace and then drives the shared runtime via Create (and
// force-recreate Remove+Create when requested). No per-agent runtime
// handle exists in the csghub build, so there is no runtime lookup /
// close phase to manage.
func (s *Service) ensureManagerBackend(ctx context.Context, forceRecreate bool, model config.ModelConfig) (sandbox.Info, string, error) {
	rt, err := s.runtimeOrOpen()
	if err != nil {
		return sandbox.Info{}, "", err
	}
	paths, mounts, err := s.gatewayMounts(ManagerName)
	if err != nil {
		return sandbox.Info{}, "", err
	}
	if _, err := ensureAgentPicoClawConfigAt(paths.PicoClaw, ManagerUserID, s.server, model); err != nil {
		return sandbox.Info{}, "", err
	}
	if _, err := ensureAgentWorkspaceAt(paths.Workspace, workspaceTemplateForAgent(ManagerName, ManagerUserID)); err != nil {
		return sandbox.Info{}, "", err
	}
	env := agentSandboxEnv(s.downstream, s.server, s.channels, ManagerUserID, model.ModelID)
	spec := buildGatewaySpec(s.managerImage, s.sandboxName(ManagerUserID), env, mounts)

	info, err := s.ensureAgentSandbox(ctx, rt, spec, forceRecreate)
	if err != nil {
		return sandbox.Info{}, "", fmt.Errorf("ensure bootstrap manager sandbox: %w", err)
	}
	return info, sandboxNameFromInfo(info, spec.Name), nil
}

// createAgent provisions a non-worker, non-manager role agent on the
// Hub. An empty image is defaulted to s.managerImage (the
// Hub-provided CSGCLAW_SANDBOX_IMAGE), matching the pre-refactor
// Service.Create behavior. Spec construction mirrors createWorker so
// per-agent workspace, picoclaw config, mounts, and env contract are
// identical for both spawn paths; Create just allows an alternative
// image override when callers want it.
func (s *Service) createAgentBackend(ctx context.Context, req createAgentRequest) (createAgentResult, error) {
	rt, err := s.runtimeOrOpen()
	if err != nil {
		return createAgentResult{}, err
	}
	image := strings.TrimSpace(req.Image)
	if image == "" {
		image = s.managerImage
	}

	paths, mounts, err := s.gatewayMounts(req.Name)
	if err != nil {
		return createAgentResult{}, err
	}
	if _, err := ensureAgentPicoClawConfigAt(paths.PicoClaw, req.ID, s.server, req.Model); err != nil {
		return createAgentResult{}, err
	}
	if _, err := ensureAgentWorkspaceAt(paths.Workspace, workspaceTemplateForAgent(req.Name, req.ID)); err != nil {
		return createAgentResult{}, err
	}
	env := agentSandboxEnv(s.downstream, s.server, s.channels, req.ID, req.Model.ModelID)
	spec := buildGatewaySpec(image, s.sandboxName(req.ID), env, mounts)

	info, err := s.ensureAgentSandbox(ctx, rt, spec, false)
	if err != nil {
		return createAgentResult{}, err
	}
	return createAgentResult{
		Info:  info,
		BoxID: sandboxNameFromInfo(info, spec.Name),
		Image: image,
	}, nil
}

// streamLogs tails gateway.log via the shared CSGHub runtime's
// StreamExecute RPC. The command string mirrors BoxLite's per-box
// `tail` invocation so operator-visible log output is identical
// across builds.
func (s *Service) streamLogsBackend(ctx context.Context, agent Agent, follow bool, lines int, w io.Writer) error {
	rt, err := s.runtimeOrOpen()
	if err != nil {
		return err
	}
	name := strings.TrimSpace(agent.BoxID)
	if name == "" {
		name = s.sandboxName(agent.ID)
	}

	cmd := fmt.Sprintf("tail -n %d %s", lines, boxPicoClawDir+"/gateway.log")
	if follow {
		cmd = fmt.Sprintf("tail -n %d -f %s", lines, boxPicoClawDir+"/gateway.log")
	}
	streamer, ok := rt.(streamExecuteRuntime)
	if !ok {
		return fmt.Errorf("sandbox provider %q runtime does not support stream execute", s.provider.Name())
	}
	return streamer.StreamExecute(ctx, name, cmd, func(line string) error {
		if _, err := io.WriteString(w, line+"\n"); err != nil {
			return err
		}
		return nil
	})
}

// WithSandboxProvider overrides the default sandbox provider.
func WithSandboxProvider(provider sandbox.Provider) ServiceOption {
	return func(s *Service) error {
		if provider == nil {
			return fmt.Errorf("sandbox provider is required")
		}
		s.provider = provider
		return nil
	}
}

// WithSandboxHomeDirName is a no-op in csghub builds; filesystem layout is
// derived from the tenant PVC env contract.
func WithSandboxHomeDirName(string) ServiceOption {
	return func(*Service) error { return nil }
}

// NewServiceWithLLMAndChannels is the canonical constructor.
func NewServiceWithLLMAndChannels(llmCfg config.LLMConfig, server config.ServerConfig, channels config.ChannelsConfig, managerImage, statePath string, opts ...ServiceOption) (*Service, error) {
	if strings.TrimSpace(managerImage) == "" {
		managerImage = config.DefaultManagerImage
	}

	defaultProfile, _ := resolveDefaultProfileAndModel(llmCfg)

	svc := &Service{
		llm:          llmCfg.Normalized(),
		server:       server,
		channels:     cloneChannelsConfig(channels),
		managerImage: managerImage,
		state:        statePath,
		provider:     defaultSandboxProvider,
		agents:       make(map[string]Agent),
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(svc); err != nil {
			return nil, err
		}
	}
	svc.applyProviderDefaults()
	if strings.TrimSpace(svc.llm.DefaultProfile) == "" {
		svc.llm.DefaultProfile = defaultProfile
	}
	if err := svc.load(); err != nil {
		return nil, err
	}
	return svc, nil
}

func (s *Service) applyProviderDefaults() {
	p, ok := s.provider.(providerAgentDefaults)
	if !ok {
		return
	}
	if img := strings.TrimSpace(p.SandboxManagerImage()); img != "" {
		s.managerImage = img
	}
	if env := p.SandboxDownstreamEnv(); len(env) > 0 {
		s.downstream = make(map[string]string, len(env))
		for k, v := range env {
			s.downstream[k] = v
		}
	}
}

func (s *Service) sandboxName(agentID string) string {
	if namer, ok := s.provider.(providerAgentNamer); ok {
		return namer.AgentSandboxName(agentID)
	}
	return "csgclaw-" + strings.TrimSpace(agentID)
}

// --- Bootstrap manager ------------------------------------------------------

func (svc *Service) EnsureBootstrapManager(ctx context.Context, forceRecreate bool) error {
	if svc == nil {
		return nil
	}
	_, defaultModel, err := svc.llm.Resolve("")
	if err != nil {
		return err
	}
	paths, _, err := svc.gatewayMounts(ManagerName)
	if err != nil {
		return err
	}
	if _, err := ensureAgentPicoClawConfigAt(paths.PicoClaw, ManagerUserID, svc.server, defaultModel); err != nil {
		return err
	}
	if _, err := ensureAgentWorkspaceAt(paths.Workspace, workspaceTemplateForAgent(ManagerName, ManagerUserID)); err != nil {
		return err
	}
	_, err = svc.EnsureManager(ctx, forceRecreate)
	return err
}

// ensureAgentSandbox drives runtime Create and
// returns runtime-neutral metadata for persistence code.
//
// Runtime implementations may expose CachedInfo() on the returned
// instance; we prefer that snapshot and only fall back to live
// Info(ctx) when unavailable.
func (s *Service) ensureAgentSandbox(ctx context.Context, rt sandbox.Runtime, spec sandbox.CreateSpec, forceRecreate bool) (sandbox.Info, error) {
	if forceRecreate {
		if err := rt.Remove(ctx, spec.Name, sandbox.RemoveOptions{Force: true}); err != nil && !sandbox.IsNotFound(err) {
			return sandbox.Info{}, fmt.Errorf("force remove sandbox %q: %w", spec.Name, err)
		}
	}
	inst, err := rt.Create(ctx, spec)
	if err != nil {
		return sandbox.Info{}, err
	}
	if cached, ok := cachedSandboxInfo(inst); ok {
		return cached, nil
	}
	info, err := inst.Info(ctx)
	if err != nil {
		return sandbox.Info{}, err
	}
	return info, nil
}

// cachedSandboxInfo unwraps a ManagedInstance that exposes a
// zero-round-trip snapshot of its last-known Response. Kept as a free
// function so ensureAgentSandbox stays oblivious to the concrete
// backend type while still benefiting from the optimization when the
// underlying instance is *csghub.Instance.
func cachedSandboxInfo(inst sandbox.Instance) (sandbox.Info, bool) {
	if c, ok := inst.(interface {
		CachedInfo() (sandbox.Info, bool)
	}); ok {
		return c.CachedInfo()
	}
	return sandbox.Info{}, false
}

func (s *Service) runtimeOrOpen() (sandbox.Runtime, error) {
	if s.runtime != nil {
		return s.runtime, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtime != nil {
		return s.runtime, nil
	}
	rt, err := s.provider.Open(context.Background(), "")
	if err != nil {
		return nil, fmt.Errorf("open csghub sandbox runtime: %w", err)
	}
	s.runtime = rt
	return rt, nil
}

func (s *Service) agentMountHostPaths(agentName string) (gatewayHostPaths, error) {
	provider, ok := s.provider.(providerAgentMountPaths)
	if !ok {
		return gatewayHostPaths{}, fmt.Errorf("sandbox provider %q does not provide agent mount paths", s.provider.Name())
	}
	picoClaw, workspace, projects, err := provider.AgentMountHostPaths(agentName)
	if err != nil {
		return gatewayHostPaths{}, err
	}
	return gatewayHostPaths{
		PicoClaw:  picoClaw,
		Workspace: workspace,
		Projects:  projects,
	}, nil
}

func (s *Service) gatewayMounts(agentName string) (gatewayHostPaths, []sandbox.Mount, error) {
	paths, err := s.agentMountHostPaths(agentName)
	if err != nil {
		return gatewayHostPaths{}, nil, err
	}
	return paths, []sandbox.Mount{
		{HostPath: paths.PicoClaw, GuestPath: boxPicoClawDir},
		{HostPath: paths.Workspace, GuestPath: boxWorkspaceDir},
		{HostPath: paths.Projects, GuestPath: boxProjectsDir},
	}, nil
}

// agentSandboxEnv assembles the env map injected into a manager or
// worker sandbox. Both roles receive the same env — they share the
// csgclaw-agent-sandbox image (FROM picoclaw + supervisor + python-
// sandbox) and only differ by sandbox name. The manager also uses this
// env to call back into the server for worker-spawn requests (D11);
// the hub admin token is NOT forwarded, worker creation goes through
// the csgclaw server HTTP API gated by CSGCLAW_ACCESS_TOKEN.
func agentSandboxEnv(downstream map[string]string, server config.ServerConfig, channels config.ChannelsConfig, botID, modelID string) map[string]string {
	managerBaseURL := resolveManagerBaseURL(server)
	llmBaseURL := llmBridgeBaseURL(managerBaseURL, botID)
	env := picoclawBoxEnvVars(managerBaseURL, server.AccessToken, botID, llmBaseURL, modelID)
	addFeishuBoxEnvVars(env, botID, channels)

	env["HOME"] = "/home/picoclaw"

	for k, v := range downstream {
		env[k] = v
	}
	return env
}

// buildGatewaySpec composes the generic sandbox.CreateSpec for a
// gateway (manager or worker) sandbox. Provisioning fields
// (ClusterID / ResourceID / Port / Timeout) are injected later by
// the csghub provider from its own Params.
func buildGatewaySpec(image, sandboxName string, env map[string]string, mounts []sandbox.Mount) sandbox.CreateSpec {
	return sandbox.CreateSpec{
		Image:  strings.TrimSpace(image),
		Name:   strings.TrimSpace(sandboxName),
		Env:    env,
		Mounts: mounts,
	}
}

// Close releases SDK resources. The HTTP client is shared-Goroutine-safe
// so there is nothing to close today, but we keep the method for parity.
func (s *Service) Close() error {
	return nil
}
