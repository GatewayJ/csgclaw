package openclawsandbox

import (
	"path"
	goruntime "runtime"
	"strings"

	agentruntime "csgclaw/internal/runtime"
)

func validateOpenClawMCPConfig(config map[string]any) error {
	return agentruntime.ValidateMCPConfig(config)
}

func openClawMCPRestartRequired(previous, current map[string]any) (bool, error) {
	return agentruntime.MCPConfigNeedsRestart(previous, current)
}

func updateOpenClawMCP(cfg map[string]any, mcpConfig map[string]any) error {
	resolved, err := resolveOpenClawMCPWorkspaceConfig(mcpConfig, workspaceGuestPathForGOOS(goruntime.GOOS))
	if err != nil {
		return err
	}
	return agentruntime.UpdateJSONMCPServers(cfg, resolved)
}

func resolveOpenClawMCPWorkspaceConfig(mcpConfig map[string]any, workspaceGuestPath string) (map[string]any, error) {
	servers, err := agentruntime.MCPConfigServers(mcpConfig)
	if err != nil {
		return nil, err
	}
	if servers == nil {
		return mcpConfig, nil
	}
	resolvedServers := make(map[string]any, len(servers))
	for name, rawEntry := range servers {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			resolvedServers[name] = rawEntry
			continue
		}
		next := make(map[string]any, len(entry))
		for key, value := range entry {
			next[key] = value
		}
		if args, ok := next["args"].([]any); ok {
			next["args"] = resolveOpenClawMCPWorkspaceArgs(args, workspaceGuestPath)
		}
		resolvedServers[name] = next
	}
	return map[string]any{agentruntime.MCPConfigServersKey: resolvedServers}, nil
}

func resolveOpenClawMCPWorkspaceArgs(args []any, workspaceGuestPath string) []any {
	workspaceGuestPath = strings.TrimSpace(workspaceGuestPath)
	if workspaceGuestPath == "" {
		return args
	}
	out := make([]any, len(args))
	for idx, arg := range args {
		text, ok := arg.(string)
		if !ok {
			out[idx] = arg
			continue
		}
		out[idx] = resolveOpenClawMCPWorkspaceArg(text, workspaceGuestPath)
	}
	return out
}

func resolveOpenClawMCPWorkspaceArg(arg, workspaceGuestPath string) string {
	for _, placeholder := range []string{"${workspace}", "${workspaceDir}", "{workspace}", "{workspaceDir}"} {
		if arg == placeholder {
			return workspaceGuestPath
		}
		if strings.HasPrefix(arg, placeholder+"/") {
			return path.Join(workspaceGuestPath, strings.TrimPrefix(arg, placeholder+"/"))
		}
	}
	return arg
}
