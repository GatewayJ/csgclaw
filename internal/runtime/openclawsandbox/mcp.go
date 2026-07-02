package openclawsandbox

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	agentruntime "csgclaw/internal/runtime"
)

type openClawMCPMode int

const (
	openClawMCPAbsent openClawMCPMode = iota
	openClawMCPCleared
	openClawMCPManaged
)

type openClawMCPConfig struct {
	mode    openClawMCPMode
	servers map[string]any
}

func validateOpenClawMCPRuntimeOptions(runtimeOptions map[string]any) error {
	_, err := openClawMCPConfigFromRuntimeOptions(runtimeOptions)
	return err
}

func openClawMCPRestartRequired(previous, current map[string]any) (bool, error) {
	prev, prevErr := openClawMCPConfigFromRuntimeOptions(previous)
	currentConfig, err := openClawMCPConfigFromRuntimeOptions(current)
	if err != nil {
		return false, err
	}
	if prevErr != nil {
		return true, nil
	}
	return !reflect.DeepEqual(openClawMCPEffectiveServers(prev), openClawMCPEffectiveServers(currentConfig)), nil
}

func updateOpenClawMCP(cfg map[string]any, runtimeOptions map[string]any) error {
	mcpConfig, err := openClawMCPConfigFromRuntimeOptions(runtimeOptions)
	if err != nil {
		return err
	}
	switch mcpConfig.mode {
	case openClawMCPAbsent:
		return nil
	case openClawMCPCleared:
		mcpRoot, ok := cfg["mcp"].(map[string]any)
		if !ok {
			return nil
		}
		delete(mcpRoot, "servers")
		if len(mcpRoot) == 0 {
			delete(cfg, "mcp")
		}
		return nil
	case openClawMCPManaged:
		mcpRoot, _ := cfg["mcp"].(map[string]any)
		if mcpRoot == nil {
			mcpRoot = map[string]any{}
			cfg["mcp"] = mcpRoot
		}
		if mcpConfig.servers == nil {
			mcpConfig.servers = map[string]any{}
		}
		mcpRoot["servers"] = mcpConfig.servers
		return nil
	default:
		return nil
	}
}

func openClawMCPEffectiveServers(config openClawMCPConfig) map[string]any {
	if config.mode != openClawMCPManaged {
		return nil
	}
	if config.servers == nil {
		return map[string]any{}
	}
	return config.servers
}

func openClawMCPConfigFromRuntimeOptions(runtimeOptions map[string]any) (openClawMCPConfig, error) {
	raw, ok := runtimeOptions[agentruntime.RuntimeOptionMCPKey]
	if !ok {
		return openClawMCPConfig{mode: openClawMCPAbsent}, nil
	}
	if raw == nil {
		return openClawMCPConfig{mode: openClawMCPCleared}, nil
	}
	mcpRoot, ok := raw.(map[string]any)
	if !ok {
		return openClawMCPConfig{}, fmt.Errorf("runtime_options.%s must be an object or null", agentruntime.RuntimeOptionMCPKey)
	}
	if unsupported := unsupportedOpenClawMCPRootKeys(mcpRoot); len(unsupported) > 0 {
		return openClawMCPConfig{}, fmt.Errorf("runtime_options.%s contains unsupported field(s) %q; only mcpServers is supported", agentruntime.RuntimeOptionMCPKey, strings.Join(unsupported, ", "))
	}
	if len(mcpRoot) == 0 {
		return openClawMCPConfig{mode: openClawMCPManaged, servers: map[string]any{}}, nil
	}
	rawServers, ok := mcpRoot["mcpServers"]
	if !ok {
		return openClawMCPConfig{mode: openClawMCPManaged, servers: map[string]any{}}, nil
	}
	serverMap, ok := rawServers.(map[string]any)
	if !ok {
		return openClawMCPConfig{}, fmt.Errorf("runtime_options.%s.mcpServers must be an object", agentruntime.RuntimeOptionMCPKey)
	}
	servers := make(map[string]any, len(serverMap))
	for rawName, rawEntry := range serverMap {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return openClawMCPConfig{}, fmt.Errorf("runtime_options.%s.mcpServers contains an empty server name", agentruntime.RuntimeOptionMCPKey)
		}
		if _, exists := servers[name]; exists {
			return openClawMCPConfig{}, fmt.Errorf("runtime_options.%s.mcpServers contains duplicate server name %q", agentruntime.RuntimeOptionMCPKey, name)
		}
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			return openClawMCPConfig{}, fmt.Errorf("runtime_options.%s.mcpServers.%s must be an object", agentruntime.RuntimeOptionMCPKey, name)
		}
		normalized, err := normalizeOpenClawMCPServerEntry(name, entry)
		if err != nil {
			return openClawMCPConfig{}, err
		}
		servers[name] = normalized
	}
	return openClawMCPConfig{mode: openClawMCPManaged, servers: servers}, nil
}

func unsupportedOpenClawMCPRootKeys(root map[string]any) []string {
	out := make([]string, 0)
	for key := range root {
		if key != "mcpServers" {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func normalizeOpenClawMCPServerEntry(name string, entry map[string]any) (map[string]any, error) {
	normalized, ok := cloneMCPJSONObject(entry).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("runtime_options.%s.mcpServers.%s must be an object", agentruntime.RuntimeOptionMCPKey, name)
	}
	command, hasCommand, err := mcpStringField(normalized, "command")
	if err != nil {
		return nil, fmt.Errorf("runtime_options.%s.mcpServers.%s.command %s", agentruntime.RuntimeOptionMCPKey, name, err)
	}
	url, hasURL, err := mcpStringField(normalized, "url")
	if err != nil {
		return nil, fmt.Errorf("runtime_options.%s.mcpServers.%s.url %s", agentruntime.RuntimeOptionMCPKey, name, err)
	}
	if !hasCommand && !hasURL {
		return nil, fmt.Errorf("runtime_options.%s.mcpServers.%s must declare command or url", agentruntime.RuntimeOptionMCPKey, name)
	}
	if hasCommand {
		normalized["command"] = command
	} else {
		delete(normalized, "command")
	}
	if hasURL {
		normalized["url"] = url
	} else {
		delete(normalized, "url")
	}
	if err := validateMCPStringSliceField(normalized, "args"); err != nil {
		return nil, fmt.Errorf("runtime_options.%s.mcpServers.%s.args must be an array of strings", agentruntime.RuntimeOptionMCPKey, name)
	}
	if err := validateMCPStringMapField(normalized, "env"); err != nil {
		return nil, fmt.Errorf("runtime_options.%s.mcpServers.%s.env must be an object with string values", agentruntime.RuntimeOptionMCPKey, name)
	}
	if err := validateMCPStringMapField(normalized, "headers"); err != nil {
		return nil, fmt.Errorf("runtime_options.%s.mcpServers.%s.headers must be an object with string values", agentruntime.RuntimeOptionMCPKey, name)
	}
	if err := validateMCPStringField(normalized, "transport"); err != nil {
		return nil, fmt.Errorf("runtime_options.%s.mcpServers.%s.transport must be a string", agentruntime.RuntimeOptionMCPKey, name)
	}
	return normalized, nil
}

func mcpStringField(values map[string]any, key string) (string, bool, error) {
	raw, ok := values[key]
	if !ok || raw == nil {
		return "", false, nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("must be a string")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false, fmt.Errorf("must not be blank")
	}
	return text, true, nil
}

func validateMCPStringField(values map[string]any, key string) error {
	raw, ok := values[key]
	if !ok || raw == nil {
		return nil
	}
	_, ok = raw.(string)
	if !ok {
		return fmt.Errorf("not a string")
	}
	return nil
}

func validateMCPStringSliceField(values map[string]any, key string) error {
	raw, ok := values[key]
	if !ok || raw == nil {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("not an array")
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			return fmt.Errorf("contains non-string value")
		}
	}
	return nil
}

func validateMCPStringMapField(values map[string]any, key string) error {
	raw, ok := values[key]
	if !ok || raw == nil {
		return nil
	}
	items, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("not an object")
	}
	for _, value := range items {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("contains non-string value")
		}
	}
	return nil
}

func cloneMCPJSONObject(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = cloneMCPJSONObject(item)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = item
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for idx, item := range typed {
			out[idx] = cloneMCPJSONObject(item)
		}
		return out
	case []string:
		out := make([]any, len(typed))
		for idx, item := range typed {
			out[idx] = item
		}
		return out
	default:
		return value
	}
}
