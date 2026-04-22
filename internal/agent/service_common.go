package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"csgclaw/internal/config"
	"csgclaw/internal/sandbox"
)

// Per-build backend lifecycle hooks are implemented directly as Service
// methods in service.go (!csghub) and service_csghub.go (csghub):
// createWorkerBackend / deleteSandboxBackend / postDeleteLockedBackend /
// streamLogsBackend / createAgentBackend / ensureManagerBackend /
// agentHomeDirBackend.

// createAgentRequest collects the already-normalized inputs the
// backend needs to provision a Create-style (non-worker) sandbox.
// Fields mirror agent.CreateRequest minus the validation / defaulting
// that common Create has already applied.
type createAgentRequest struct {
	Name  string
	ID    string
	Image string
	Model config.ModelConfig
}

// createAgentResult carries runtime metadata common Create needs to
// assemble the persisted Agent record. Image lets the backend report
// the image it actually used (CSGHub may override when req.Image is
// empty); BoxID is the sandbox identifier to persist (empty string
// preserves BoxLite's legacy "no BoxID" behavior); Info is used for
// CreatedAt / Status fallback when req does not supply them.
type createAgentResult struct {
	Info  sandbox.Info
	BoxID string
	Image string
}

// ServiceOption configures a Service during construction. The concrete
// Service struct differs between build tags, so options must live in a
// build-tag-specific file whenever they touch backend fields; pure
// option wiring can live here.
type ServiceOption func(*Service) error

// NewService constructs a default single-profile LLM Service.
func NewService(model config.ModelConfig, server config.ServerConfig, managerImage, statePath string, opts ...ServiceOption) (*Service, error) {
	return NewServiceWithLLM(config.SingleProfileLLM(model), server, managerImage, statePath, opts...)
}

// NewServiceWithChannels accepts the legacy single-profile + channels signature.
func NewServiceWithChannels(model config.ModelConfig, server config.ServerConfig, channels config.ChannelsConfig, managerImage, statePath string, opts ...ServiceOption) (*Service, error) {
	return NewServiceWithLLMAndChannels(config.SingleProfileLLM(model), server, channels, managerImage, statePath, opts...)
}

// NewServiceWithLLM accepts a multi-profile LLM config without channels.
func NewServiceWithLLM(llmCfg config.LLMConfig, server config.ServerConfig, managerImage, statePath string, opts ...ServiceOption) (*Service, error) {
	return NewServiceWithLLMAndChannels(llmCfg, server, config.ChannelsConfig{}, managerImage, statePath, opts...)
}

// EnsureBootstrapState is the legacy entrypoint used by cmd/csgclaw onboarding.
func EnsureBootstrapState(ctx context.Context, statePath string, server config.ServerConfig, model config.ModelConfig, managerImage string, forceRecreate bool) error {
	return EnsureBootstrapStateWithLLM(ctx, statePath, server, config.SingleProfileLLM(model), managerImage, forceRecreate)
}

// EnsureBootstrapStateWithLLM constructs a service, guarantees the bootstrap
// manager sandbox, and closes the service.
func EnsureBootstrapStateWithLLM(ctx context.Context, statePath string, server config.ServerConfig, llmCfg config.LLMConfig, managerImage string, forceRecreate bool) error {
	svc, err := NewServiceWithLLM(llmCfg, server, managerImage, statePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = svc.Close()
	}()
	return svc.EnsureBootstrapManager(ctx, forceRecreate)
}

// resolveDefaultProfileAndModel returns the effective default profile name and
// resolved model snapshot used to initialize Service defaults. When LLM
// resolution fails (no model configured yet) we still produce a best-effort
// profile name so the constructor can proceed.
func resolveDefaultProfileAndModel(llmCfg config.LLMConfig) (string, config.ModelConfig) {
	defaultProfile, model, err := llmCfg.Resolve("")
	if err == nil {
		return defaultProfile, model
	}
	defaultProfile = strings.TrimSpace(llmCfg.Normalized().Default)
	if defaultProfile == "" {
		defaultProfile = strings.TrimSpace(llmCfg.Normalized().DefaultProfile)
	}
	return defaultProfile, config.ModelConfig{}.Resolved()
}

// --- Public agent API shared across builds ----------------------------------

// CreateWorker creates a worker agent using the backend strategy for
// the active build tag. Worker agents are the common case driven by
// `csgclaw agents create` and the IM layer; they always use the
// Service-default manager image and derive id from name via the
// "u-<name>" convention.
func (s *Service) CreateWorker(ctx context.Context, req CreateRequest) (Agent, error) {
	id := strings.TrimSpace(req.ID)
	name := strings.TrimSpace(req.Name)
	description := strings.TrimSpace(req.Description)
	switch {
	case name == "":
		return Agent{}, fmt.Errorf("name is required")
	case strings.EqualFold(name, ManagerName):
		return Agent{}, fmt.Errorf("name %q is reserved", name)
	}
	if id == "" {
		id = fmt.Sprintf("u-%s", name)
	}

	s.mu.RLock()
	_, idExists := s.agents[id]
	nameExists := s.hasNameLocked(name)
	s.mu.RUnlock()
	if idExists {
		return Agent{}, fmt.Errorf("agent id %q already exists", id)
	}
	if nameExists {
		return Agent{}, fmt.Errorf("agent name %q already exists", name)
	}

	profileName, resolvedModel, err := s.resolveModelProfile(req.Profile)
	if err != nil {
		return Agent{}, err
	}

	info, boxID, err := s.createWorkerBackend(ctx, name, id, resolvedModel)
	if err != nil {
		return Agent{}, fmt.Errorf("create worker box: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.agents[id]; ok {
		return Agent{}, fmt.Errorf("agent id %q already exists", id)
	}
	if s.hasNameLocked(name) {
		return Agent{}, fmt.Errorf("agent name %q already exists", name)
	}

	createdAt := info.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	worker := Agent{
		ID:              id,
		Name:            name,
		Image:           s.managerImage,
		BoxID:           boxID,
		Description:     description,
		Status:          string(info.State),
		CreatedAt:       createdAt,
		Profile:         profileName,
		Provider:        resolvedModel.Provider,
		ModelID:         resolvedModel.ModelID,
		ReasoningEffort: resolvedModel.ReasoningEffort,
		Role:            RoleWorker,
	}
	s.agents[worker.ID] = worker
	if err := s.saveLocked(); err != nil {
		delete(s.agents, worker.ID)
		return Agent{}, err
	}
	return *cloneAgent(&worker), nil
}

// Delete removes an agent and the sandbox backing it. The persisted
// registry update happens last and only after the sandbox and the
// on-disk agent home have been cleaned up; a sandbox "not found"
// response is swallowed so callers can retry safely.
//
// The `manager` agent is reserved and cannot be deleted through this
// path; use EnsureManager(force=true) to rebuild it.
func (s *Service) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("agent id is required")
	}

	s.mu.RLock()
	existing, ok := s.agents[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("agent %q not found", id)
	}
	if isManagerAgent(existing) {
		return fmt.Errorf("agent %q is reserved", id)
	}

	if err := s.deleteSandboxBackend(ctx, existing); err != nil {
		return err
	}

	agentHome, err := s.agentHomeDirBackend(existing.Name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(agentHome); err != nil {
		return fmt.Errorf("remove agent home: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.agents[id]
	if !ok {
		return fmt.Errorf("agent %q not found", id)
	}
	delete(s.agents, id)
	s.postDeleteLockedBackend(current)
	return s.saveLocked()
}

// Create registers a non-worker, non-manager role agent. The heavy
// lifting (image defaulting, sandbox spec construction, Hub round
// trips) lives in backend.createAgent; common logic here handles
// validation, id generation, duplicate detection, LLM profile
// resolution, CreatedAt/Status fallback, and registry persistence.
//
// Semantics preserved across builds:
//   - a blank req.ID defaults to "<role>-<unixnano>",
//   - role == manager is reserved,
//   - duplicate id or case-insensitive name returns an error,
//   - when req does not pin CreatedAt/Status, the backend's reported
//     sandbox.Info is used; when that is also blank we fall back to
//     time.Now() / "running" so legacy BoxLite callers are unaffected.
func (s *Service) Create(ctx context.Context, req CreateRequest) (Agent, error) {
	id := strings.TrimSpace(req.ID)
	name := strings.TrimSpace(req.Name)
	description := strings.TrimSpace(req.Description)
	image := strings.TrimSpace(req.Image)
	role := normalizeRole(req.Role)
	if name == "" {
		return Agent{}, fmt.Errorf("name is required")
	}
	if role == RoleManager {
		return Agent{}, fmt.Errorf("role %q is reserved", role)
	}
	if id == "" {
		id = fmt.Sprintf("%s-%d", role, time.Now().UnixNano())
	}

	s.mu.RLock()
	_, idExists := s.agents[id]
	nameExists := s.hasNameLocked(name)
	s.mu.RUnlock()
	if idExists {
		return Agent{}, fmt.Errorf("agent id %q already exists", id)
	}
	if nameExists {
		return Agent{}, fmt.Errorf("agent name %q already exists", name)
	}

	requestedProfile := strings.TrimSpace(req.Profile)
	if requestedProfile == "" && strings.TrimSpace(req.ModelID) != "" {
		matchedProfile, _, ok := s.llm.MatchProfile(config.ModelConfig{ModelID: req.ModelID})
		if !ok {
			return Agent{}, fmt.Errorf("no llm profile matches model %q", strings.TrimSpace(req.ModelID))
		}
		requestedProfile = matchedProfile
	}
	profileName, resolvedModel, err := s.resolveModelProfile(requestedProfile)
	if err != nil {
		return Agent{}, err
	}

	result, err := s.createAgentBackend(ctx, createAgentRequest{
		Name:  name,
		ID:    id,
		Image: image,
		Model: resolvedModel,
	})
	if err != nil {
		return Agent{}, err
	}

	effectiveImage := strings.TrimSpace(result.Image)
	if effectiveImage == "" {
		effectiveImage = image
	}

	createdAt := req.CreatedAt.UTC()
	if createdAt.IsZero() {
		if !result.Info.CreatedAt.IsZero() {
			createdAt = result.Info.CreatedAt.UTC()
		} else {
			createdAt = time.Now().UTC()
		}
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = strings.TrimSpace(string(result.Info.State))
	}
	if status == "" {
		status = "running"
	}

	agent := Agent{
		ID:              id,
		Name:            name,
		Description:     description,
		Image:           effectiveImage,
		BoxID:           result.BoxID,
		Role:            role,
		Status:          status,
		CreatedAt:       createdAt,
		Profile:         profileName,
		Provider:        resolvedModel.Provider,
		ModelID:         resolvedModel.ModelID,
		ReasoningEffort: resolvedModel.ReasoningEffort,
	}

	s.mu.Lock()
	s.agents[id] = agent
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		return Agent{}, err
	}
	return agent, nil
}

// EnsureManager guarantees that the bootstrap manager sandbox exists
// and is running, then upserts the manager entry in the registry. It
// also evicts any stale manager-role agents with a different ID so
// the registry never carries multiple managers (can happen if state
// migrated from a previous ManagerUserID convention).
//
// The heavy sandbox lifecycle lives in backend.ensureManager, which
// differs substantially between builds (BoxLite: per-agent runtime
// lookup + image-pull progress logging + rebuild-on-force; CSGHub:
// shared runtime Create with optional Remove+Create on force). Common
// logic here is only the registry assembly and persistence.
func (s *Service) EnsureManager(ctx context.Context, forceRecreate bool) (Agent, error) {
	if s == nil {
		return Agent{}, fmt.Errorf("agent service is required")
	}
	defaultProfile, defaultModel, err := s.llm.Resolve("")
	if err != nil {
		return Agent{}, err
	}

	info, boxID, err := s.ensureManagerBackend(ctx, forceRecreate, defaultModel)
	if err != nil {
		return Agent{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	createdAt := info.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	manager := Agent{
		ID:              ManagerUserID,
		Name:            ManagerName,
		Image:           s.managerImage,
		BoxID:           boxID,
		Status:          string(info.State),
		CreatedAt:       createdAt,
		Profile:         defaultProfile,
		Provider:        defaultModel.Resolved().Provider,
		ModelID:         defaultModel.Resolved().ModelID,
		ReasoningEffort: defaultModel.Resolved().ReasoningEffort,
		Role:            RoleManager,
	}
	for id, a := range s.agents {
		if isManagerAgent(a) && id != manager.ID {
			delete(s.agents, id)
		}
	}
	s.agents[manager.ID] = manager
	if err := s.saveLocked(); err != nil {
		return Agent{}, err
	}
	return *cloneAgent(&manager), nil
}

// StreamLogs tails the gateway log for the given agent, writing lines
// to w. When follow is true the stream stays open until ctx is
// cancelled or the sandbox terminates. Validation (trim, default line
// count, agent lookup) lives here so both backends share identical
// error messages; the actual log source differs per backend and is
// handled by backend.streamLogs.
func (s *Service) StreamLogs(ctx context.Context, id string, follow bool, lines int, w io.Writer) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("agent id is required")
	}
	if w == nil {
		return fmt.Errorf("log writer is required")
	}
	if lines <= 0 {
		lines = 20
	}

	got, ok := s.Agent(id)
	if !ok {
		return fmt.Errorf("agent %q not found", id)
	}

	return s.streamLogsBackend(ctx, got, follow, lines, w)
}

// --- Generic accessors shared across builds ---------------------------------
//
// These methods only touch the backend-neutral fields on *Service
// (`s.mu`, `s.agents`) and helpers defined in model.go. They used to
// live duplicated in service.go and service_csghub.go; consolidating
// them here lets the per-build files focus on sandbox lifecycle logic.

// Agent returns a copy of the agent with the given id, if present.
func (s *Service) Agent(id string) (Agent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	a, ok := s.agents[strings.TrimSpace(id)]
	if !ok {
		return Agent{}, false
	}
	return *cloneAgent(&a), true
}

// List returns every known agent sorted by id.
func (s *Service) List() []Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedAgentsFromMap(s.agents)
}

// ListWorkers returns every worker agent sorted by id.
func (s *Service) ListWorkers() []Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workers := make(map[string]Agent)
	for id, a := range s.agents {
		if a.Role == RoleWorker {
			workers[id] = a
		}
	}
	return sortedAgentsFromMap(workers)
}

// hasNameLocked reports whether any agent is already registered under
// the given name (case-insensitive). Caller must hold s.mu.
func (s *Service) hasNameLocked(name string) bool {
	for _, existing := range s.agents {
		if strings.EqualFold(existing.Name, name) {
			return true
		}
	}
	return false
}

// sandboxNameFromInfo returns the most stable identifier for a sandbox
// given backend-neutral metadata, preferring Name over ID and falling
// back to the caller-supplied value when both are empty.
func sandboxNameFromInfo(info sandbox.Info, fallback string) string {
	if v := strings.TrimSpace(info.Name); v != "" {
		return v
	}
	if v := strings.TrimSpace(info.ID); v != "" {
		return v
	}
	return strings.TrimSpace(fallback)
}

// cloneChannelsConfig returns a deep-ish copy of channels so Service
// construction does not share the caller's Feishu map.
func cloneChannelsConfig(channels config.ChannelsConfig) config.ChannelsConfig {
	cloned := config.ChannelsConfig{
		FeishuAdminOpenID: channels.FeishuAdminOpenID,
	}
	if len(channels.Feishu) > 0 {
		cloned.Feishu = make(map[string]config.FeishuConfig, len(channels.Feishu))
		for name, feishu := range channels.Feishu {
			cloned.Feishu[name] = feishu
		}
	}
	return cloned
}
