package openclawsandbox

import (
	agentruntime "csgclaw/internal/runtime"
)

func validateOpenClawMCPConfig(config map[string]any) error {
	return agentruntime.ValidateMCPConfig(config)
}

func openClawMCPRestartRequired(previous, current map[string]any) (bool, error) {
	return agentruntime.MCPConfigNeedsRestart(previous, current)
}

func updateOpenClawMCP(cfg map[string]any, mcpConfig map[string]any) error {
	return agentruntime.UpdateJSONMCPServers(cfg, mcpConfig)
}
