package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	agentruntime "csgclaw/internal/runtime"
)

const ServersKey = agentruntime.MCPConfigServersKey

func normalizeServerInput(name string, config map[string]any) (string, map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, fmt.Errorf("mcp server name is required")
	}
	if config == nil {
		return "", nil, fmt.Errorf("mcp server config is required")
	}

	serverConfig, err := unwrapServerConfig(name, config)
	if err != nil {
		return "", nil, err
	}
	normalized, err := agentruntime.NormalizeMCPConfig(map[string]any{
		ServersKey: map[string]any{name: serverConfig},
	})
	if err != nil {
		return "", nil, err
	}
	servers, err := serversFromConfig(normalized)
	if err != nil {
		return "", nil, err
	}
	normalizedServer, ok := servers[name].(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("mcp server config for %q must be an object", name)
	}
	return name, cloneMap(normalizedServer), nil
}

func unwrapServerConfig(name string, config map[string]any) (map[string]any, error) {
	rawServers, ok := config[ServersKey]
	if !ok || rawServers == nil {
		return config, nil
	}
	servers, ok := rawServers.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcp server config mcpServers must be an object")
	}
	if rawConfig, ok := servers[name]; ok {
		serverConfig, ok := rawConfig.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mcp server config for %q must be an object", name)
		}
		return serverConfig, nil
	}
	if len(servers) == 1 {
		for _, rawConfig := range servers {
			serverConfig, ok := rawConfig.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("mcp server config mcpServers entry must be an object")
			}
			return serverConfig, nil
		}
	}
	return nil, fmt.Errorf("mcp server config mcpServers must contain exactly one server or a server named %q", name)
}

func serversFromConfig(config map[string]any) (map[string]any, error) {
	rawServers, ok := config[ServersKey]
	if !ok || rawServers == nil {
		return map[string]any{}, nil
	}
	servers, ok := rawServers.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcp state mcpServers must be an object")
	}
	return servers, nil
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	data, err := json.Marshal(value)
	if err != nil {
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = item
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}
