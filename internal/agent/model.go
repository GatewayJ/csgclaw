package agent

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	agentruntime "csgclaw/internal/runtime"
	"csgclaw/internal/utils"
)

const (
	RoleAgent   = "agent"
	RoleWorker  = "worker"
	RoleManager = "manager"
)

const StatusRuntimeUnavailable = "runtime_unavailable"

type Agent struct {
	ID               string                   `json:"id"`
	Name             string                   `json:"name"`
	Description      string                   `json:"description,omitempty"`
	Instructions     string                   `json:"instructions,omitempty"`
	RuntimeID        string                   `json:"runtime_id,omitempty"`
	RuntimeKind      string                   `json:"runtime_kind,omitempty"`
	RuntimeName      string                   `json:"runtime_name,omitempty"`
	SandboxEnabled   bool                     `json:"sandbox_enabled,omitempty"`
	Image            string                   `json:"image,omitempty"`
	Avatar           string                   `json:"avatar,omitempty"`
	BoxID            string                   `json:"box_id,omitempty"`
	RuntimeOptions   map[string]any           `json:"runtime_options,omitempty"`
	MCPConfig        map[string]any           `json:"mcp_config,omitempty"`
	Role             string                   `json:"role"`
	Status           string                   `json:"status"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at,omitempty"`
	Profile          string                   `json:"profile,omitempty"`
	AgentProfile     AgentProfile             `json:"agent_profile,omitempty"`
	ProfileComplete  bool                     `json:"profile_complete"`
	DetectionResults []ProfileDetectionResult `json:"detection_results,omitempty"`
}

func (a Agent) RuntimeConfig() agentruntime.RuntimeConfig {
	if cfg, err := agentruntime.RuntimeConfigFromSelection(a.RuntimeKind, a.RuntimeName, a.SandboxEnabled); err == nil {
		return cfg
	}
	return agentruntime.RuntimeConfig{Name: a.RuntimeName, Sandboxed: a.SandboxEnabled}.Normalized()
}

func (a *Agent) SetRuntimeConfig(cfg agentruntime.RuntimeConfig) {
	if a == nil {
		return
	}
	cfg = cfg.Normalized()
	a.RuntimeName = cfg.Name
	a.SandboxEnabled = cfg.Sandboxed
	a.RuntimeKind = cfg.LegacyKind()
}

type runtimeJSON struct {
	ID             string             `json:"id,omitempty"`
	Kind           string             `json:"kind,omitempty"`
	Name           string             `json:"name,omitempty"`
	SandboxEnabled *bool              `json:"sandbox_enabled,omitempty"`
	State          agentruntime.State `json:"state,omitempty"`
	SandboxID      string             `json:"sandbox_id,omitempty"`
	Options        map[string]any     `json:"options,omitempty"`
}

func (a *Agent) UnmarshalJSON(data []byte) error {
	type agentJSON struct {
		ID               string                   `json:"id"`
		Name             string                   `json:"name"`
		Description      string                   `json:"description,omitempty"`
		Instructions     string                   `json:"instructions,omitempty"`
		RuntimeID        string                   `json:"runtime_id,omitempty"`
		RuntimeKind      string                   `json:"runtime_kind,omitempty"`
		RuntimeName      string                   `json:"runtime_name,omitempty"`
		SandboxEnabled   *bool                    `json:"sandbox_enabled,omitempty"`
		Runtime          *runtimeJSON             `json:"runtime,omitempty"`
		Image            string                   `json:"image,omitempty"`
		Avatar           string                   `json:"avatar,omitempty"`
		BoxID            string                   `json:"box_id,omitempty"`
		RuntimeOptions   map[string]any           `json:"runtime_options,omitempty"`
		MCPConfig        map[string]any           `json:"mcp_config,omitempty"`
		Role             string                   `json:"role"`
		Status           string                   `json:"status"`
		CreatedAt        time.Time                `json:"created_at"`
		UpdatedAt        time.Time                `json:"updated_at,omitempty"`
		ModelConfig      json.RawMessage          `json:"model_config"`
		Profile          json.RawMessage          `json:"profile"`
		AgentProfile     AgentProfile             `json:"agent_profile,omitempty"`
		ProfileComplete  bool                     `json:"profile_complete"`
		DetectionResults []ProfileDetectionResult `json:"detection_results,omitempty"`
	}
	var decoded agentJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	out := Agent{
		ID:               decoded.ID,
		Name:             decoded.Name,
		Description:      decoded.Description,
		Instructions:     decoded.Instructions,
		RuntimeID:        decoded.RuntimeID,
		RuntimeKind:      decoded.RuntimeKind,
		RuntimeName:      decoded.RuntimeName,
		Image:            decoded.Image,
		Avatar:           decoded.Avatar,
		BoxID:            decoded.BoxID,
		RuntimeOptions:   utils.CloneAnyMap(decoded.RuntimeOptions),
		MCPConfig:        utils.CloneAnyMap(decoded.MCPConfig),
		Role:             decoded.Role,
		Status:           decoded.Status,
		CreatedAt:        decoded.CreatedAt,
		UpdatedAt:        decoded.UpdatedAt,
		AgentProfile:     cloneProfile(decoded.AgentProfile),
		ProfileComplete:  decoded.ProfileComplete,
		DetectionResults: append([]ProfileDetectionResult(nil), decoded.DetectionResults...),
	}
	if decoded.SandboxEnabled != nil {
		out.SandboxEnabled = *decoded.SandboxEnabled
	}
	if decoded.Runtime != nil {
		rt := normalizeRuntimeRecord(RuntimeRecord{
			ID:        decoded.Runtime.ID,
			Kind:      decoded.Runtime.Kind,
			State:     decoded.Runtime.State,
			SandboxID: decoded.Runtime.SandboxID,
			Options:   decoded.Runtime.Options,
		})
		if strings.TrimSpace(out.RuntimeID) == "" && strings.TrimSpace(rt.ID) != "" {
			out.RuntimeID = rt.ID
		}
		if strings.TrimSpace(out.RuntimeKind) == "" {
			out.RuntimeKind = rt.Kind
		}
		if strings.TrimSpace(out.BoxID) == "" {
			out.BoxID = rt.SandboxID
		}
		if strings.TrimSpace(out.Status) == "" && rt.State != "" {
			out.Status = string(rt.State)
		}
		if len(out.RuntimeOptions) == 0 && len(rt.Options) > 0 {
			out.RuntimeOptions = utils.CloneAnyMap(rt.Options)
		}
		if strings.TrimSpace(out.RuntimeName) == "" {
			out.RuntimeName = strings.TrimSpace(decoded.Runtime.Name)
		}
		if decoded.Runtime.SandboxEnabled != nil {
			out.SandboxEnabled = *decoded.Runtime.SandboxEnabled
		}
	}
	out.RuntimeOptions, out.MCPConfig = splitLegacyRuntimeOptionsMCP(out.RuntimeOptions, out.MCPConfig)
	out.SetRuntimeConfig(out.RuntimeConfig())
	profilePayload := decoded.ModelConfig
	if len(profilePayload) == 0 || string(profilePayload) == "null" {
		profilePayload = decoded.Profile
	}
	if len(profilePayload) > 0 && string(profilePayload) != "null" {
		var profile AgentProfile
		if err := json.Unmarshal(profilePayload, &profile); err == nil {
			out.AgentProfile = profile
			out.Profile = profileSelector(profile)
		} else {
			var selector string
			if err := json.Unmarshal(profilePayload, &selector); err != nil {
				return err
			}
			out.Profile = strings.TrimSpace(selector)
		}
	}
	*a = out
	return nil
}

type CreateAgentSpec struct {
	ID             string         `json:"id,omitempty"`
	Name           string         `json:"name"`
	Description    string         `json:"description,omitempty"`
	Instructions   string         `json:"instructions,omitempty"`
	Image          string         `json:"image,omitempty"`
	Avatar         string         `json:"-"`
	RuntimeKind    string         `json:"-"`
	RuntimeName    string         `json:"runtime_name,omitempty"`
	SandboxEnabled bool           `json:"sandbox_enabled,omitempty"`
	FromTemplate   string         `json:"from_template,omitempty"`
	Role           string         `json:"role,omitempty"`
	Status         string         `json:"status,omitempty"`
	CreatedAt      time.Time      `json:"created_at,omitempty"`
	UpdatedAt      time.Time      `json:"updated_at,omitempty"`
	Profile        string         `json:"profile,omitempty"`
	RuntimeOptions map[string]any `json:"runtime_options,omitempty"`
	MCPConfig      map[string]any `json:"mcp_config,omitempty"`
	MCPConfigSet   bool           `json:"-"`
	AgentProfile   AgentProfile   `json:"agent_profile,omitempty"`
}

func (s CreateAgentSpec) RuntimeConfig() agentruntime.RuntimeConfig {
	if cfg, err := agentruntime.RuntimeConfigFromSelection(s.RuntimeKind, s.RuntimeName, s.SandboxEnabled); err == nil {
		return cfg
	}
	return agentruntime.RuntimeConfig{Name: s.RuntimeName, Sandboxed: s.SandboxEnabled}.Normalized()
}

func (s *CreateAgentSpec) SetRuntimeConfig(cfg agentruntime.RuntimeConfig) {
	if s == nil {
		return
	}
	cfg = cfg.Normalized()
	s.RuntimeName = cfg.Name
	s.SandboxEnabled = cfg.Sandboxed
	s.RuntimeKind = cfg.LegacyKind()
}

func (s CreateAgentSpec) MarshalJSON() ([]byte, error) {
	type createAgentSpecJSON struct {
		ID             string `json:"id,omitempty"`
		Name           string `json:"name"`
		Description    string `json:"description,omitempty"`
		Instructions   string `json:"instructions,omitempty"`
		Image          string `json:"image,omitempty"`
		RuntimeName    string `json:"runtime_name,omitempty"`
		SandboxEnabled bool   `json:"sandbox_enabled,omitempty"`
		Runtime        *struct {
			Name           string         `json:"name,omitempty"`
			SandboxEnabled bool           `json:"sandbox_enabled,omitempty"`
			Options        map[string]any `json:"options,omitempty"`
		} `json:"runtime,omitempty"`
		FromTemplate   string         `json:"from_template,omitempty"`
		Role           string         `json:"role,omitempty"`
		Status         string         `json:"status,omitempty"`
		CreatedAt      time.Time      `json:"created_at,omitempty"`
		UpdatedAt      time.Time      `json:"updated_at,omitempty"`
		Profile        string         `json:"profile,omitempty"`
		RuntimeOptions map[string]any `json:"runtime_options,omitempty"`
		MCPConfig      map[string]any `json:"mcp_config,omitempty"`
		AgentProfile   AgentProfile   `json:"agent_profile,omitempty"`
	}
	runtimeName := strings.TrimSpace(s.RuntimeName)
	sandboxEnabled := s.SandboxEnabled
	if runtimeName == "" {
		runtimeName = agentruntime.RuntimeConfigForKind(s.RuntimeKind).Name
	}
	if !sandboxEnabled {
		sandboxEnabled = agentruntime.SandboxEnabledForKind(s.RuntimeKind)
	}
	var runtime *struct {
		Name           string         `json:"name,omitempty"`
		SandboxEnabled bool           `json:"sandbox_enabled,omitempty"`
		Options        map[string]any `json:"options,omitempty"`
	}
	if runtimeName != "" || sandboxEnabled || len(s.RuntimeOptions) > 0 {
		runtime = &struct {
			Name           string         `json:"name,omitempty"`
			SandboxEnabled bool           `json:"sandbox_enabled,omitempty"`
			Options        map[string]any `json:"options,omitempty"`
		}{
			Name:           runtimeName,
			SandboxEnabled: sandboxEnabled,
			Options:        utils.CloneAnyMap(s.RuntimeOptions),
		}
	}
	return json.Marshal(createAgentSpecJSON{
		ID:             s.ID,
		Name:           s.Name,
		Description:    s.Description,
		Instructions:   s.Instructions,
		Image:          s.Image,
		RuntimeName:    runtimeName,
		SandboxEnabled: sandboxEnabled,
		Runtime:        runtime,
		FromTemplate:   s.FromTemplate,
		Role:           s.Role,
		Status:         s.Status,
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
		Profile:        s.Profile,
		RuntimeOptions: utils.CloneAnyMap(s.RuntimeOptions),
		MCPConfig:      utils.CloneAnyMap(s.MCPConfig),
		AgentProfile:   cloneProfile(s.AgentProfile),
	})
}

func (s *CreateAgentSpec) UnmarshalJSON(data []byte) error {
	type createAgentSpecJSON struct {
		ID             string          `json:"id,omitempty"`
		Name           string          `json:"name"`
		Description    string          `json:"description,omitempty"`
		Instructions   string          `json:"instructions,omitempty"`
		Image          string          `json:"image,omitempty"`
		Avatar         string          `json:"-"`
		RuntimeName    string          `json:"runtime_name,omitempty"`
		SandboxEnabled *bool           `json:"sandbox_enabled,omitempty"`
		Runtime        *runtimeJSON    `json:"runtime,omitempty"`
		FromTemplate   string          `json:"from_template,omitempty"`
		Role           string          `json:"role,omitempty"`
		Status         string          `json:"status,omitempty"`
		CreatedAt      time.Time       `json:"created_at,omitempty"`
		UpdatedAt      time.Time       `json:"updated_at,omitempty"`
		ModelConfig    json.RawMessage `json:"model_config,omitempty"`
		Profile        json.RawMessage `json:"profile,omitempty"`
		RuntimeOptions map[string]any  `json:"runtime_options,omitempty"`
		MCPConfig      map[string]any  `json:"mcp_config,omitempty"`
		AgentProfile   AgentProfile    `json:"agent_profile,omitempty"`
	}
	var decoded createAgentSpecJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	out := CreateAgentSpec{
		ID:             decoded.ID,
		Name:           decoded.Name,
		Description:    decoded.Description,
		Instructions:   decoded.Instructions,
		Image:          decoded.Image,
		RuntimeName:    decoded.RuntimeName,
		FromTemplate:   decoded.FromTemplate,
		Role:           decoded.Role,
		Status:         decoded.Status,
		CreatedAt:      decoded.CreatedAt,
		UpdatedAt:      decoded.UpdatedAt,
		RuntimeOptions: utils.CloneAnyMap(decoded.RuntimeOptions),
		MCPConfig:      utils.CloneAnyMap(decoded.MCPConfig),
		AgentProfile:   cloneProfile(decoded.AgentProfile),
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawFields); err == nil {
		if _, ok := rawFields["mcp_config"]; ok {
			out.MCPConfigSet = true
		}
	}
	if decoded.SandboxEnabled != nil {
		out.SandboxEnabled = *decoded.SandboxEnabled
	}
	if decoded.Runtime != nil {
		rt := normalizeRuntimeRecord(RuntimeRecord{
			Kind:      decoded.Runtime.Kind,
			State:     decoded.Runtime.State,
			SandboxID: decoded.Runtime.SandboxID,
			Options:   decoded.Runtime.Options,
		})
		if strings.TrimSpace(out.RuntimeKind) == "" {
			out.RuntimeKind = rt.Kind
		}
		if strings.TrimSpace(out.Status) == "" && rt.State != "" {
			out.Status = string(rt.State)
		}
		if len(out.RuntimeOptions) == 0 && len(rt.Options) > 0 {
			out.RuntimeOptions = utils.CloneAnyMap(rt.Options)
		}
		if strings.TrimSpace(out.RuntimeName) == "" {
			out.RuntimeName = strings.TrimSpace(decoded.Runtime.Name)
		}
		if decoded.Runtime.SandboxEnabled != nil {
			out.SandboxEnabled = *decoded.Runtime.SandboxEnabled
		}
	}
	var legacyMCPSet bool
	out.RuntimeOptions, out.MCPConfig, legacyMCPSet, _ = splitLegacyRuntimeOptionsMCPValue(out.RuntimeOptions, out.MCPConfig, out.MCPConfigSet, false)
	if legacyMCPSet {
		out.MCPConfigSet = true
	}
	out.SetRuntimeConfig(out.RuntimeConfig())
	profilePayload := decoded.ModelConfig
	if len(profilePayload) == 0 || string(profilePayload) == "null" {
		profilePayload = decoded.Profile
	}
	if len(profilePayload) > 0 && string(profilePayload) != "null" {
		var profile AgentProfile
		if err := json.Unmarshal(profilePayload, &profile); err == nil {
			out.AgentProfile = profile
			out.Profile = profileSelector(profile)
		} else {
			var selector string
			if err := json.Unmarshal(profilePayload, &selector); err != nil {
				return err
			}
			out.Profile = strings.TrimSpace(selector)
		}
	}
	*s = out
	return nil
}

type UpdateRequest struct {
	Name                      *string         `json:"name,omitempty"`
	Description               *string         `json:"description,omitempty"`
	Instructions              *string         `json:"instructions,omitempty"`
	Image                     *string         `json:"image,omitempty"`
	Avatar                    *string         `json:"-"`
	Profile                   *string         `json:"profile,omitempty"`
	RuntimeKind               string          `json:"-"`
	RuntimeName               string          `json:"-"`
	SandboxEnabled            *bool           `json:"-"`
	RuntimeSelectionRequested bool            `json:"-"`
	RuntimeOptions            *map[string]any `json:"runtime_options,omitempty"`
	MCPConfig                 *map[string]any `json:"mcp_config,omitempty"`
	MCPConfigSet              bool            `json:"-"`
	AgentProfile              *AgentProfile   `json:"agent_profile,omitempty"`
	FieldMask                 []string        `json:"field_mask,omitempty"`
}

func (r *UpdateRequest) UnmarshalJSON(data []byte) error {
	type runtimeUpdateJSON struct {
		Kind           string         `json:"kind,omitempty"`
		Name           string         `json:"name,omitempty"`
		SandboxEnabled *bool          `json:"sandbox_enabled,omitempty"`
		Options        map[string]any `json:"options,omitempty"`
	}
	type updateRequestJSON struct {
		Name           *string            `json:"name,omitempty"`
		Description    *string            `json:"description,omitempty"`
		Instructions   *string            `json:"instructions,omitempty"`
		Image          *string            `json:"image,omitempty"`
		Avatar         *string            `json:"-"`
		RuntimeKind    string             `json:"runtime_kind,omitempty"`
		RuntimeName    string             `json:"runtime_name,omitempty"`
		SandboxEnabled *bool              `json:"sandbox_enabled,omitempty"`
		ModelConfig    json.RawMessage    `json:"model_config,omitempty"`
		Profile        json.RawMessage    `json:"profile,omitempty"`
		Runtime        *runtimeUpdateJSON `json:"runtime,omitempty"`
		RuntimeOptions *map[string]any    `json:"runtime_options,omitempty"`
		MCPConfig      *map[string]any    `json:"mcp_config,omitempty"`
		AgentProfile   *AgentProfile      `json:"agent_profile,omitempty"`
		FieldMask      []string           `json:"field_mask,omitempty"`
	}
	var decoded updateRequestJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	out := UpdateRequest{
		Name:           decoded.Name,
		Description:    decoded.Description,
		Instructions:   decoded.Instructions,
		Image:          decoded.Image,
		RuntimeKind:    strings.TrimSpace(decoded.RuntimeKind),
		RuntimeName:    strings.TrimSpace(decoded.RuntimeName),
		SandboxEnabled: decoded.SandboxEnabled,
		RuntimeOptions: decoded.RuntimeOptions,
		MCPConfig:      decoded.MCPConfig,
		AgentProfile:   decoded.AgentProfile,
		FieldMask:      append([]string(nil), decoded.FieldMask...),
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawFields); err == nil {
		if raw, ok := rawFields["mcp_config"]; ok {
			out.MCPConfigSet = true
			if string(raw) != "null" {
				var cfg map[string]any
				if err := json.Unmarshal(raw, &cfg); err != nil {
					return err
				}
				out.MCPConfig = &cfg
			}
		}
	}
	profileField := ""
	profilePayload := decoded.ModelConfig
	if len(profilePayload) == 0 || string(profilePayload) == "null" {
		profilePayload = decoded.Profile
	}
	if len(profilePayload) > 0 && string(profilePayload) != "null" {
		var profile AgentProfile
		if err := json.Unmarshal(profilePayload, &profile); err == nil {
			out.AgentProfile = &profile
			profileField = "agent_profile"
		} else {
			var selector string
			if err := json.Unmarshal(profilePayload, &selector); err != nil {
				return err
			}
			selector = strings.TrimSpace(selector)
			out.Profile = &selector
			profileField = "profile"
		}
	}
	if decoded.Runtime != nil {
		if strings.TrimSpace(out.RuntimeKind) == "" {
			out.RuntimeKind = strings.TrimSpace(decoded.Runtime.Kind)
		}
		if strings.TrimSpace(out.RuntimeName) == "" {
			out.RuntimeName = strings.TrimSpace(decoded.Runtime.Name)
		}
		if out.SandboxEnabled == nil {
			out.SandboxEnabled = decoded.Runtime.SandboxEnabled
		}
		if len(decoded.Runtime.Options) > 0 {
			options := utils.CloneAnyMap(decoded.Runtime.Options)
			out.RuntimeOptions = &options
		}
	}
	rawRuntimeSelectionRequested := strings.TrimSpace(out.RuntimeKind) != "" ||
		strings.TrimSpace(out.RuntimeName) != "" ||
		out.SandboxEnabled != nil
	out.RuntimeSelectionRequested = rawRuntimeSelectionRequested && updateFieldMaskRequestsRuntimeSelection(out.FieldMask)
	if out.RuntimeOptions != nil {
		var existingMCPConfig map[string]any
		if out.MCPConfig != nil {
			existingMCPConfig = *out.MCPConfig
		}
		options, mcpConfig, legacyMCPSet, err := splitLegacyRuntimeOptionsMCPStrictWithPresence(*out.RuntimeOptions, existingMCPConfig, out.MCPConfigSet)
		if err != nil {
			return err
		}
		out.RuntimeOptions = &options
		if !out.MCPConfigSet && legacyMCPSet {
			if mcpConfig != nil {
				out.MCPConfig = &mcpConfig
			}
			out.MCPConfigSet = true
		}
	}
	if len(out.FieldMask) > 0 {
		out.FieldMask = normalizeCompactUpdateFieldMask(out.FieldMask, profileField, decoded.Runtime != nil)
	}
	*r = out
	return nil
}

func updateFieldMaskRequestsRuntimeSelection(fieldMask []string) bool {
	if len(fieldMask) == 0 {
		return true
	}
	for _, field := range fieldMask {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "runtime", "runtime_kind", "runtime_name", "sandbox_enabled":
			return true
		}
	}
	return false
}

func normalizeCompactUpdateFieldMask(fieldMask []string, profileField string, hasRuntime bool) []string {
	if len(fieldMask) == 0 {
		return nil
	}
	out := make([]string, 0, len(fieldMask))
	seen := map[string]struct{}{}
	add := func(field string) {
		field = strings.ToLower(strings.TrimSpace(field))
		if field == "" {
			return
		}
		if _, ok := seen[field]; ok {
			return
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	for _, field := range fieldMask {
		normalized := strings.ToLower(strings.TrimSpace(field))
		switch normalized {
		case "profile", "model_config":
			if profileField != "" {
				add(profileField)
			} else {
				add(normalized)
			}
		case "runtime":
			if hasRuntime {
				add("runtime_options")
			} else {
				add(normalized)
			}
		default:
			add(normalized)
		}
	}
	return out
}

type CreateRequest struct {
	Spec      CreateAgentSpec
	Replace   bool
	FieldMask []string
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", RoleAgent:
		return RoleAgent
	case RoleWorker:
		return RoleWorker
	case RoleManager:
		return RoleManager
	default:
		return strings.ToLower(strings.TrimSpace(role))
	}
}

func isManagerAgent(a Agent) bool {
	return strings.EqualFold(strings.TrimSpace(a.Role), RoleManager) ||
		strings.EqualFold(strings.TrimSpace(a.Name), ManagerName) ||
		strings.EqualFold(strings.TrimSpace(a.ID), ManagerUserID)
}

func sortedAgentsFromMap(items map[string]Agent) []Agent {
	agents := make([]Agent, 0, len(items))
	for _, a := range items {
		agents = append(agents, *cloneAgent(&a))
	}
	slices.SortFunc(agents, func(a, b Agent) int {
		if a.CreatedAt.Equal(b.CreatedAt) {
			switch {
			case a.ID < b.ID:
				return -1
			case a.ID > b.ID:
				return 1
			default:
				return 0
			}
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return -1
		}
		return 1
	})
	return agents
}

func persistedAgentsFromMap(items map[string]Agent) []persistedAgent {
	agents := sortedAgentsFromMap(items)
	persisted := make([]persistedAgent, 0, len(agents))
	for _, a := range agents {
		persisted = append(persisted, newPersistedAgent(a))
	}
	return persisted
}

func cloneAgent(src *Agent) *Agent {
	if src == nil {
		return nil
	}
	dst := *src
	dst.AgentProfile = cloneProfile(src.AgentProfile)
	dst.DetectionResults = append([]ProfileDetectionResult(nil), src.DetectionResults...)
	dst.RuntimeOptions = utils.CloneAnyMap(src.RuntimeOptions)
	dst.MCPConfig = utils.CloneAnyMap(src.MCPConfig)
	return &dst
}

func splitLegacyRuntimeOptionsMCP(runtimeOptions map[string]any, mcpConfig map[string]any) (map[string]any, map[string]any) {
	options, currentMCP, _, _ := splitLegacyRuntimeOptionsMCPValue(runtimeOptions, mcpConfig, mcpConfig != nil, false)
	return options, currentMCP
}

func runtimeOptionsHasLegacyMCP(runtimeOptions map[string]any) bool {
	if runtimeOptions == nil {
		return false
	}
	_, ok := runtimeOptions[agentruntime.RuntimeOptionMCPKey]
	return ok
}

func splitLegacyRuntimeOptionsMCPStrict(runtimeOptions map[string]any, mcpConfig map[string]any, mcpConfigProvided bool) (map[string]any, map[string]any, error) {
	options, currentMCP, _, err := splitLegacyRuntimeOptionsMCPValue(runtimeOptions, mcpConfig, mcpConfigProvided, true)
	return options, currentMCP, err
}

func splitLegacyRuntimeOptionsMCPStrictWithPresence(runtimeOptions map[string]any, mcpConfig map[string]any, mcpConfigProvided bool) (map[string]any, map[string]any, bool, error) {
	return splitLegacyRuntimeOptionsMCPValue(runtimeOptions, mcpConfig, mcpConfigProvided, true)
}

func splitLegacyRuntimeOptionsMCPValue(runtimeOptions map[string]any, mcpConfig map[string]any, mcpConfigProvided, strict bool) (map[string]any, map[string]any, bool, error) {
	options := utils.CloneAnyMap(runtimeOptions)
	currentMCP := cloneAnyMapPreserveEmpty(mcpConfig)
	if options == nil {
		return nil, currentMCP, false, nil
	}
	raw, ok := options[agentruntime.RuntimeOptionMCPKey]
	if !ok {
		if len(options) == 0 {
			return nil, currentMCP, false, nil
		}
		return options, currentMCP, false, nil
	}
	delete(options, agentruntime.RuntimeOptionMCPKey)
	if !mcpConfigProvided {
		if raw == nil {
			currentMCP = nil
		} else {
			rawMap, ok := raw.(map[string]any)
			if !ok {
				if strict {
					return nil, nil, true, fmt.Errorf("runtime_options.%s must be an object or null", agentruntime.RuntimeOptionMCPKey)
				}
			} else if normalized, err := agentruntime.NormalizeMCPConfig(rawMap); err == nil {
				currentMCP = normalized
			} else if strict {
				return nil, nil, true, legacyRuntimeOptionsMCPErrorPath(err)
			} else {
				currentMCP = cloneAnyMapPreserveEmpty(rawMap)
			}
		}
	}
	if len(options) == 0 {
		options = nil
	}
	return options, currentMCP, true, nil
}

func legacyRuntimeOptionsMCPErrorPath(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ReplaceAll(err.Error(), "mcp_config", "runtime_options."+agentruntime.RuntimeOptionMCPKey)
	return fmt.Errorf("%s", message)
}

func cloneAnyMapPreserveEmpty(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}
