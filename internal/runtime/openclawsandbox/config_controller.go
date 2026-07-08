package openclawsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	agentruntime "csgclaw/internal/runtime"
	"csgclaw/internal/runtime/sandboxgateway"
)

var _ agentruntime.RuntimeConfigController = (*Runtime)(nil)
var _ agentruntime.MCPConfigController = (*Runtime)(nil)
var _ agentruntime.MCPConfigListController = (*Runtime)(nil)

func (r *Runtime) ValidateConfig(_ context.Context, current agentruntime.RuntimeConfigSnapshot) error {
	return nil
}

func (r *Runtime) RestartRequired(change agentruntime.RuntimeConfigChange) (bool, error) {
	return false, nil
}

func (r *Runtime) ReconcileConfig(_ context.Context, h agentruntime.Handle, _ agentruntime.RuntimeConfigChange) error {
	agentRef, err := r.ResolveAgentForHandle(h)
	if err != nil {
		return err
	}
	agentHome, err := r.AgentHomeForAgentID(agentRef.ID)
	if err != nil {
		return err
	}
	return refreshWorkspaceAgentsFile(filepath.Join(r.Layout(agentHome).WorkspaceRoot, "AGENTS.md"), agentRef.Instructions)
}

func (r *Runtime) ValidateMCPConfig(_ context.Context, current agentruntime.MCPConfigSnapshot) error {
	return validateOpenClawMCPConfig(current.Config)
}

func (r *Runtime) MCPConfigRestartRequired(change agentruntime.MCPConfigChange) (bool, error) {
	return openClawMCPRestartRequired(change.Previous.Config, change.Current.Config)
}

func (r *Runtime) ReconcileMCPConfig(_ context.Context, h agentruntime.Handle, change agentruntime.MCPConfigChange) error {
	prepared, err := r.PreparedGatewayProvisionForHandle(h)
	if errors.Is(err, sandboxgateway.ErrPreparedGatewayProvisionNotAvailable) {
		return nil
	}
	if err != nil {
		return err
	}
	agentHome, err := r.AgentHomeForHandle(h)
	if err != nil {
		return err
	}
	profile := prepared.Profile.Normalized()
	if profile.ModelID == "" {
		profile.ModelID = prepared.ModelID
	}
	_, err = EnsureConfigWithMCPConfig(
		agentHome,
		prepared.ParticipantID,
		prepared.AgentID,
		prepared.Server,
		configModelFromProfile(profile),
		change.Current.Config,
		fixedBaseURL(prepared.ManagerBaseURL),
		r.CurrentFeishuProvider(),
	)
	return err
}

func (r *Runtime) ListMCPConfig(_ context.Context, h agentruntime.Handle, _ agentruntime.MCPConfigSnapshot) (agentruntime.MCPConfigSnapshot, error) {
	agentHome, err := r.AgentHomeForHandle(h)
	if err != nil {
		return agentruntime.MCPConfigSnapshot{}, err
	}
	return readOpenClawMCPConfig(filepath.Join(Root(agentHome), HostConfig))
}

func readOpenClawMCPConfig(path string) (agentruntime.MCPConfigSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return agentruntime.MCPConfigSnapshot{}, nil
		}
		return agentruntime.MCPConfigSnapshot{}, fmt.Errorf("read openclaw mcp config: %w", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return agentruntime.MCPConfigSnapshot{}, fmt.Errorf("decode openclaw mcp config: %w", err)
	}
	rawMCPRoot, ok := cfg["mcp"]
	if !ok || rawMCPRoot == nil {
		return agentruntime.MCPConfigSnapshot{}, nil
	}
	mcpRoot, ok := rawMCPRoot.(map[string]any)
	if !ok {
		return agentruntime.MCPConfigSnapshot{}, fmt.Errorf("openclaw mcp config mcp must be an object")
	}
	rawServers, ok := mcpRoot["servers"]
	if !ok || rawServers == nil {
		return agentruntime.MCPConfigSnapshot{}, nil
	}
	servers, ok := rawServers.(map[string]any)
	if !ok {
		return agentruntime.MCPConfigSnapshot{}, fmt.Errorf("openclaw mcp config servers must be an object")
	}
	normalized, err := agentruntime.NormalizeMCPConfig(map[string]any{agentruntime.MCPConfigServersKey: servers})
	if err != nil {
		return agentruntime.MCPConfigSnapshot{}, err
	}
	return agentruntime.MCPConfigSnapshot{Config: normalized}, nil
}
