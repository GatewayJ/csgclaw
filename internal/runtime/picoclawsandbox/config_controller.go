package picoclawsandbox

import (
	"context"

	agentruntime "csgclaw/internal/runtime"
)

var _ agentruntime.MCPConfigController = (*Runtime)(nil)

func (r *Runtime) ValidateMCPConfig(_ context.Context, current agentruntime.MCPConfigSnapshot) error {
	return agentruntime.ValidateMCPConfig(current.Config)
}

func (r *Runtime) MCPConfigRestartRequired(change agentruntime.MCPConfigChange) (bool, error) {
	return agentruntime.MCPConfigNeedsRestart(change.Previous.Config, change.Current.Config)
}

func (r *Runtime) ReconcileMCPConfig(context.Context, agentruntime.Handle, agentruntime.MCPConfigChange) error {
	return nil
}
