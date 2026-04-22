//go:build !csghub
// +build !csghub

package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"csgclaw/internal/config"
	"csgclaw/internal/sandbox"
)

var defaultSandboxProvider sandbox.Provider = unconfiguredSandboxProvider{}

type unconfiguredSandboxProvider struct{}

func (unconfiguredSandboxProvider) Name() string {
	return "unconfigured"
}

func (unconfiguredSandboxProvider) Open(context.Context, string) (sandbox.Runtime, error) {
	return nil, fmt.Errorf("sandbox provider is not configured")
}

var (
	testEnsureRuntimeHook       func(*Service, string) (sandbox.Runtime, error)
	testEnsureRuntimeAtHomeHook func(*Service, string) (sandbox.Runtime, error)
	testGetBoxHook              func(*Service, context.Context, sandbox.Runtime, string) (sandbox.Instance, error)
	testStartBoxHook            func(*Service, context.Context, sandbox.Instance) error
	testBoxInfoHook             func(*Service, context.Context, sandbox.Instance) (sandbox.Info, error)
	testCloseBoxHook            func(*Service, sandbox.Instance) error
	testCloseRuntimeHook        func(*Service, string, sandbox.Runtime) error
	testCreateBoxHook           func(*Service, context.Context, sandbox.Runtime, sandbox.CreateSpec) (sandbox.Instance, error)
	testCreateGatewayBoxHook    func(*Service, context.Context, sandbox.Runtime, string, string, string, config.ModelConfig) (sandbox.Instance, sandbox.Info, error)
	testForceRemoveBoxHook      func(*Service, context.Context, sandbox.Runtime, string) error
	testRunBoxCommandHook       func(*Service, context.Context, sandbox.Instance, string, []string, io.Writer) (int, error)
)

// SetTestHooks installs lightweight hooks for tests that need to bypass runtime/box creation.
func SetTestHooks(
	ensureRuntime func(*Service, string) (sandbox.Runtime, error),
	createGatewayBox func(*Service, context.Context, sandbox.Runtime, string, string, string, config.ModelConfig) (sandbox.Instance, sandbox.Info, error),
) {
	testEnsureRuntimeHook = ensureRuntime
	if ensureRuntime != nil {
		testEnsureRuntimeAtHomeHook = func(s *Service, _ string) (sandbox.Runtime, error) {
			return ensureRuntime(s, ManagerName)
		}
	} else {
		testEnsureRuntimeAtHomeHook = nil
	}
	testCreateGatewayBoxHook = createGatewayBox
}

// ResetTestHooks clears hooks installed via SetTestHooks.
func ResetTestHooks() {
	testEnsureRuntimeHook = nil
	testEnsureRuntimeAtHomeHook = nil
	testGetBoxHook = nil
	testStartBoxHook = nil
	testBoxInfoHook = nil
	testCloseBoxHook = nil
	testCloseRuntimeHook = nil
	testCreateBoxHook = nil
	testCreateGatewayBoxHook = nil
	testForceRemoveBoxHook = nil
	testRunBoxCommandHook = nil
}

// TestOnlySetSandboxProvider replaces the default sandbox provider for newly
// created services. It returns a restore function for test cleanup.
func TestOnlySetSandboxProvider(provider sandbox.Provider) func() {
	previous := defaultSandboxProvider
	if provider == nil {
		defaultSandboxProvider = unconfiguredSandboxProvider{}
	} else {
		defaultSandboxProvider = provider
	}
	return func() {
		defaultSandboxProvider = previous
	}
}

// TestOnlySetGetBoxHook installs a test hook for box lookup.
func TestOnlySetGetBoxHook(hook func(*Service, context.Context, sandbox.Runtime, string) (sandbox.Instance, error)) {
	testGetBoxHook = hook
}

// TestOnlySetRunBoxCommandHook installs a test hook for command execution inside a box.
func TestOnlySetRunBoxCommandHook(hook func(*Service, context.Context, sandbox.Instance, string, []string, io.Writer) (int, error)) {
	testRunBoxCommandHook = hook
}

type Service struct {
	model        config.ModelConfig
	llm          config.LLMConfig
	server       config.ServerConfig
	channels     config.ChannelsConfig
	managerImage string
	state        string
	sandbox      sandbox.Provider
	sandboxHome  string
	mu           sync.RWMutex
	runtimes     map[string]sandbox.Runtime
	agents       map[string]Agent
}

func (s *Service) createWorkerBackend(ctx context.Context, name, id string, model config.ModelConfig) (sandbox.Info, string, error) {
	rt, err := s.ensureRuntime(name)
	if err != nil {
		return sandbox.Info{}, "", err
	}
	runtimeHome, err := s.sandboxRuntimeHome(name)
	if err != nil {
		return sandbox.Info{}, "", err
	}
	defer func() { _ = s.closeRuntime(runtimeHome, rt) }()

	box, info, err := s.createGatewayBox(ctx, rt, s.managerImage, name, id, model)
	if err != nil {
		return sandbox.Info{}, "", err
	}
	defer func() { _ = s.closeBox(box) }()

	return info, info.ID, nil
}

func (s *Service) deleteSandboxBackend(ctx context.Context, agent Agent) error {
	rt, err := s.ensureRuntime(agent.Name)
	if err != nil {
		return err
	}
	runtimeHome, err := s.sandboxRuntimeHome(agent.Name)
	if err != nil {
		return err
	}
	// testEnsureRuntimeHook may return (nil, nil) to simulate "no live
	// runtime, nothing to tear down". Honor that so the registry-only
	// delete path stays exercisable from tests without a full fake.
	if rt == nil {
		return nil
	}
	boxIDOrName := strings.TrimSpace(agent.BoxID)
	if boxIDOrName == "" {
		boxIDOrName = agent.Name
	}
	if _, resolvedKey, resolveErr := s.resolveAgentBox(ctx, rt, agent); resolveErr == nil && strings.TrimSpace(resolvedKey) != "" {
		boxIDOrName = resolvedKey
	}
	if err := s.forceRemoveBox(ctx, rt, boxIDOrName); err != nil && !sandbox.IsNotFound(err) {
		return fmt.Errorf("remove agent box: %w", err)
	}
	_ = s.closeRuntime(runtimeHome, rt)
	return nil
}

// postDeleteLockedBackend prunes the per-agent runtime map in case
// deleteSandboxBackend returned before reaching closeRuntime (e.g. the
// test-only rt == nil short-circuit) and a stale entry is still
// cached. closeRuntime already performs this prune atomically on
// the happy path; this is defensive bookkeeping that mirrors the
// pre-refactor behavior of Service.Delete.
func (s *Service) postDeleteLockedBackend(agent Agent) {
	home, err := s.sandboxRuntimeHome(agent.Name)
	if err != nil {
		return
	}
	if _, ok := s.runtimes[home]; ok {
		delete(s.runtimes, home)
	}
}

func (s *Service) agentHomeDirBackend(agentName string) (string, error) {
	return agentHomeDir(agentName)
}

// ensureManager drives the BoxLite-specific bootstrap flow:
// lookup via the ManagerName runtime, optional force-recreate
// (remove box + drop runtime + wipe per-agent home + recreate
// runtime), create-or-start branching, and background image-pull
// progress logging. The heavy side-effect lifecycle lives here so
// common EnsureManager only cares about registry persistence.
func (s *Service) ensureManagerBackend(ctx context.Context, forceRecreate bool, model config.ModelConfig) (sandbox.Info, string, error) {
	rt, box, err := s.lookupBootstrapManager(ctx)
	if err != nil {
		return sandbox.Info{}, "", err
	}
	runtimeHome, err := s.sandboxRuntimeHome(ManagerName)
	if err != nil {
		return sandbox.Info{}, "", err
	}
	defer func() {
		_ = s.closeRuntime(runtimeHome, rt)
	}()

	if forceRecreate {
		log.Printf("force recreating bootstrap manager box %q", ManagerName)
		managerBoxIDOrName := s.bootstrapManagerBoxIDOrName()
		if err := s.forceRemoveBox(ctx, rt, managerBoxIDOrName); err != nil {
			if sandbox.IsNotFound(err) {
				log.Printf("bootstrap manager box %q (%q) does not exist yet; continuing", ManagerName, managerBoxIDOrName)
			} else {
				return sandbox.Info{}, "", fmt.Errorf("force remove bootstrap manager box %q (%q): %w", ManagerName, managerBoxIDOrName, err)
			}
		} else {
			log.Printf("bootstrap manager box %q (%q) removed", ManagerName, managerBoxIDOrName)
		}
		if err := s.closeRuntime(runtimeHome, rt); err != nil {
			return sandbox.Info{}, "", fmt.Errorf("close bootstrap manager runtime before recreate: %w", err)
		}
		rt = nil
		managerHome, err := agentHomeDir(ManagerName)
		if err != nil {
			return sandbox.Info{}, "", err
		}
		if err := os.RemoveAll(managerHome); err != nil {
			return sandbox.Info{}, "", fmt.Errorf("remove bootstrap manager home: %w", err)
		}
		rt, err = s.ensureRuntimeAtHome(runtimeHome)
		if err != nil {
			return sandbox.Info{}, "", err
		}
		box = nil
	}

	var info sandbox.Info
	if box == nil {
		log.Printf("bootstrap manager box %q not found, creating it with image %q", ManagerName, s.managerImage)
		log.Printf("if the image is not present locally, the first pull may take a while")
		progressDone := make(chan struct{})
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-progressDone:
					return
				case <-ticker.C:
					log.Printf("still creating bootstrap manager box %q with image %q; image download may still be in progress", ManagerName, s.managerImage)
				}
			}
		}()
		box, info, err = s.createGatewayBox(ctx, rt, s.managerImage, ManagerName, ManagerUserID, model)
		close(progressDone)
		if err != nil {
			return sandbox.Info{}, "", fmt.Errorf("create bootstrap manager box: %w", err)
		}
		log.Printf("bootstrap manager box %q created", ManagerName)
	} else {
		if err := s.startBox(ctx, box); err != nil {
			return sandbox.Info{}, "", fmt.Errorf("start bootstrap manager box: %w", err)
		}
		info, err = s.boxInfo(ctx, box)
		if err != nil {
			return sandbox.Info{}, "", fmt.Errorf("read bootstrap manager box info: %w", err)
		}
	}
	defer func() {
		_ = s.closeBox(box)
	}()

	return info, info.ID, nil
}

// createAgent provisions a non-worker, non-manager role agent via the
// BoxLite per-agent runtime. An empty image is rejected (BoxLite has
// no "managed" default image to fall back to, unlike csghub). The
// returned result intentionally leaves BoxID blank and Info zero so
// that common Create applies its legacy time.Now()/"running"
// fallback — this preserves pre-refactor behavior where
// Service.Create never persisted a BoxID and always stamped
// Status="running" regardless of sandbox state.
func (s *Service) createAgentBackend(ctx context.Context, req createAgentRequest) (createAgentResult, error) {
	image := strings.TrimSpace(req.Image)
	if image == "" {
		return createAgentResult{}, fmt.Errorf("image is required")
	}

	rt, err := s.ensureRuntime(req.Name)
	if err != nil {
		return createAgentResult{}, err
	}
	runtimeHome, err := s.sandboxRuntimeHome(req.Name)
	if err != nil {
		return createAgentResult{}, err
	}
	defer func() { _ = s.closeRuntime(runtimeHome, rt) }()

	projectsRoot, err := ensureAgentProjectsRoot()
	if err != nil {
		return createAgentResult{}, err
	}
	managerBaseURL := resolveManagerBaseURL(s.server)
	llmBaseURL := llmBridgeBaseURL(managerBaseURL, req.ID)
	boxSpec := sandbox.CreateSpec{
		Image:      image,
		Name:       req.Name,
		Detach:     true,
		AutoRemove: false,
		Mounts: []sandbox.Mount{
			{HostPath: projectsRoot, GuestPath: boxProjectsDir},
		},
		Env: make(map[string]string),
	}
	for key, value := range bridgeLLMEnvVars(llmBaseURL, s.server.AccessToken, req.Model.ModelID) {
		boxSpec.Env[key] = value
	}
	box, err := s.createBox(ctx, rt, boxSpec)
	if err != nil {
		return createAgentResult{}, fmt.Errorf("create sandbox agent: %w", err)
	}
	defer func() { _ = s.closeBox(box) }()

	return createAgentResult{Image: image}, nil
}

// streamLogs opens a per-agent runtime, resolves the live box, and
// tails gateway.log via an in-box `tail` exec. The sandbox runtime
// (for this agent) and the resolved box handle are closed on return
// so the logs call does not leak resources even when ctx is cancelled
// mid-stream.
func (s *Service) streamLogsBackend(ctx context.Context, agent Agent, follow bool, lines int, w io.Writer) error {
	rt, err := s.ensureRuntime(agent.Name)
	if err != nil {
		return err
	}
	runtimeHome, err := s.sandboxRuntimeHome(agent.Name)
	if err != nil {
		return err
	}
	defer func() { _ = s.closeRuntime(runtimeHome, rt) }()

	box, resolvedKey, err := s.resolveAgentBox(ctx, rt, agent)
	if err != nil {
		if sandbox.IsNotFound(err) {
			boxIDOrName := strings.TrimSpace(agent.BoxID)
			if boxIDOrName == "" {
				boxIDOrName = agent.Name
			}
			return fmt.Errorf("agent box %q not found", boxIDOrName)
		}
		return err
	}
	defer func() { _ = s.closeBox(box) }()
	if err := s.refreshAgentBoxID(agent, resolvedKey, box); err != nil {
		return err
	}

	args := []string{"-n", fmt.Sprintf("%d", lines)}
	if follow {
		args = append(args, "-f")
	}
	args = append(args, boxPicoClawDir+"/gateway.log")

	exitCode, err := s.runBoxCommand(ctx, box, "tail", args, w)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("tail exited with code %d", exitCode)
	}
	return nil
}

func WithSandboxProvider(provider sandbox.Provider) ServiceOption {
	return func(s *Service) error {
		if provider == nil {
			return fmt.Errorf("sandbox provider is required")
		}
		s.sandbox = provider
		return nil
	}
}

func WithSandboxHomeDirName(name string) ServiceOption {
	return func(s *Service) error {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("sandbox home dir name is required")
		}
		s.sandboxHome = name
		return nil
	}
}

func NewServiceWithLLMAndChannels(llmCfg config.LLMConfig, server config.ServerConfig, channels config.ChannelsConfig, managerImage, statePath string, opts ...ServiceOption) (*Service, error) {
	// agent.Service owns the persisted registry and the live sandbox lifecycle.
	if managerImage == "" {
		managerImage = config.DefaultManagerImage
	}
	defaultProfile, model := resolveDefaultProfileAndModel(llmCfg)
	svc := &Service{
		model:        model,
		llm:          llmCfg.Normalized(),
		server:       server,
		channels:     cloneChannelsConfig(channels),
		managerImage: managerImage,
		state:        statePath,
		sandbox:      defaultSandboxProvider,
		sandboxHome:  config.DefaultSandboxHomeDirName,
		runtimes:     make(map[string]sandbox.Runtime),
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
	if strings.TrimSpace(svc.llm.DefaultProfile) == "" {
		svc.llm.DefaultProfile = defaultProfile
	}
	if err := svc.load(); err != nil {
		return nil, err
	}
	return svc, nil
}

func (svc *Service) EnsureBootstrapManager(ctx context.Context, forceRecreate bool) error {
	if svc == nil {
		return nil
	}
	_, defaultModel, err := svc.llm.Resolve("")
	if err != nil {
		return err
	}
	if _, err := ensureAgentPicoClawConfig(ManagerName, ManagerUserID, svc.server, defaultModel); err != nil {
		return err
	}

	_, err = svc.EnsureManager(ctx, forceRecreate)
	return err
}

func (s *Service) bootstrapManagerBoxIDOrName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, a := range s.agents {
		if !isManagerAgent(a) {
			continue
		}
		if boxID := strings.TrimSpace(a.BoxID); boxID != "" {
			return boxID
		}
	}
	return ManagerName
}

func (s *Service) resolveAgentBox(ctx context.Context, rt sandbox.Runtime, got Agent) (sandbox.Instance, string, error) {
	keys := make([]string, 0, 2)
	if boxID := strings.TrimSpace(got.BoxID); boxID != "" {
		keys = append(keys, boxID)
	}
	if name := strings.TrimSpace(got.Name); name != "" {
		if len(keys) == 0 || keys[0] != name {
			keys = append(keys, name)
		}
	}
	if len(keys) == 0 {
		return nil, "", fmt.Errorf("agent box identifier is required")
	}

	var lastNotFound error
	for _, key := range keys {
		box, err := s.getBox(ctx, rt, key)
		if err == nil {
			return box, key, nil
		}
		if sandbox.IsNotFound(err) {
			lastNotFound = err
			continue
		}
		return nil, "", fmt.Errorf("get agent box: %w", err)
	}
	if lastNotFound != nil {
		return nil, strings.TrimSpace(got.BoxID), lastNotFound
	}
	return nil, "", fmt.Errorf("agent box %q not found", got.Name)
}

func (s *Service) refreshAgentBoxID(got Agent, resolvedKey string, box sandbox.Instance) error {
	if box == nil {
		return nil
	}
	if strings.TrimSpace(got.BoxID) != "" && strings.TrimSpace(got.BoxID) == strings.TrimSpace(resolvedKey) {
		return nil
	}

	info, err := s.boxInfo(context.Background(), box)
	if err != nil {
		return fmt.Errorf("read agent box info: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.agents[got.ID]
	if !ok {
		return nil
	}
	if strings.TrimSpace(current.BoxID) == info.ID {
		return nil
	}
	current.BoxID = info.ID
	s.agents[got.ID] = current
	return s.saveLocked()
}

func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var closeErr error
	for name, rt := range s.runtimes {
		if err := rt.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
		delete(s.runtimes, name)
	}
	return closeErr
}
