package openclawsandbox

import (
	"context"
	"path/filepath"

	agentruntime "csgclaw/internal/runtime"
)

var _ agentruntime.RuntimeConfigController = (*Runtime)(nil)
var _ agentruntime.MCPConfigController = (*Runtime)(nil)

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

func (r *Runtime) ReconcileMCPConfig(context.Context, agentruntime.Handle, agentruntime.MCPConfigChange) error {
	return nil
}
