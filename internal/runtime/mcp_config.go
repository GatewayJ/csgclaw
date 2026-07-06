package runtime

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

const MCPConfigServersKey = "mcpServers"

type MCPConfigSnapshot struct {
	Config map[string]any
}

type MCPConfigChange struct {
	Previous MCPConfigSnapshot
	Current  MCPConfigSnapshot
}

type MCPConfigController interface {
	ValidateMCPConfig(ctx context.Context, current MCPConfigSnapshot) error
	MCPConfigRestartRequired(change MCPConfigChange) (bool, error)
	ReconcileMCPConfig(ctx context.Context, h Handle, change MCPConfigChange) error
}

type MCPConfigMode int

const (
	MCPConfigAbsent MCPConfigMode = iota
	MCPConfigManaged
)

type MCPConfig struct {
	Mode    MCPConfigMode
	Servers map[string]any
}

func NormalizeMCPConfig(raw map[string]any) (map[string]any, error) {
	parsed, err := DecodeMCPConfig(raw, "mcp_config")
	if err != nil {
		return nil, err
	}
	if parsed.Mode == MCPConfigAbsent {
		return nil, nil
	}
	servers := parsed.Servers
	if servers == nil {
		servers = map[string]any{}
	}
	return map[string]any{MCPConfigServersKey: servers}, nil
}

func ValidateMCPConfig(raw map[string]any) error {
	_, err := DecodeMCPConfig(raw, "mcp_config")
	return err
}

func MCPConfigServers(raw map[string]any) (map[string]any, error) {
	parsed, err := DecodeMCPConfig(raw, "mcp_config")
	if err != nil {
		return nil, err
	}
	if parsed.Mode != MCPConfigManaged {
		return nil, nil
	}
	if parsed.Servers == nil {
		return map[string]any{}, nil
	}
	return parsed.Servers, nil
}

func MCPConfigNeedsRestart(previous, current map[string]any) (bool, error) {
	prev, prevErr := DecodeMCPConfig(previous, "mcp_config")
	next, err := DecodeMCPConfig(current, "mcp_config")
	if err != nil {
		return false, err
	}
	if prevErr != nil {
		return true, nil
	}
	return !reflect.DeepEqual(MCPConfigEffectiveServers(prev), MCPConfigEffectiveServers(next)), nil
}

func MCPConfigEffectiveServers(config MCPConfig) map[string]any {
	if config.Mode != MCPConfigManaged {
		return nil
	}
	if config.Servers == nil {
		return map[string]any{}
	}
	return config.Servers
}

func UpdateJSONMCPServers(cfg map[string]any, raw map[string]any) error {
	servers, err := MCPConfigServers(raw)
	if err != nil {
		return err
	}
	if servers == nil {
		mcpRoot, ok := cfg["mcp"].(map[string]any)
		if !ok {
			return nil
		}
		delete(mcpRoot, "servers")
		if len(mcpRoot) == 0 {
			delete(cfg, "mcp")
		}
		return nil
	}
	mcpRoot, _ := cfg["mcp"].(map[string]any)
	if mcpRoot == nil {
		mcpRoot = map[string]any{}
		cfg["mcp"] = mcpRoot
	}
	mcpRoot["servers"] = servers
	return nil
}

func DecodeMCPConfig(raw map[string]any, path string) (MCPConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "mcp_config"
	}
	if raw == nil {
		return MCPConfig{Mode: MCPConfigAbsent}, nil
	}
	if unsupported := unsupportedMCPRootKeys(raw); len(unsupported) > 0 {
		return MCPConfig{}, fmt.Errorf("%s contains unsupported field(s) %q; only %s is supported", path, strings.Join(unsupported, ", "), MCPConfigServersKey)
	}
	if len(raw) == 0 {
		return MCPConfig{Mode: MCPConfigManaged, Servers: map[string]any{}}, nil
	}
	rawServers, ok := raw[MCPConfigServersKey]
	if !ok {
		return MCPConfig{Mode: MCPConfigManaged, Servers: map[string]any{}}, nil
	}
	serverMap, ok := rawServers.(map[string]any)
	if !ok {
		return MCPConfig{}, fmt.Errorf("%s.%s must be an object", path, MCPConfigServersKey)
	}
	servers := make(map[string]any, len(serverMap))
	for rawName, rawEntry := range serverMap {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return MCPConfig{}, fmt.Errorf("%s.%s contains an empty server name", path, MCPConfigServersKey)
		}
		if _, exists := servers[name]; exists {
			return MCPConfig{}, fmt.Errorf("%s.%s contains duplicate server name %q", path, MCPConfigServersKey, name)
		}
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			return MCPConfig{}, fmt.Errorf("%s.%s.%s must be an object", path, MCPConfigServersKey, name)
		}
		normalized, err := normalizeMCPServerEntry(path, name, entry)
		if err != nil {
			return MCPConfig{}, err
		}
		servers[name] = normalized
	}
	return MCPConfig{Mode: MCPConfigManaged, Servers: servers}, nil
}

func unsupportedMCPRootKeys(root map[string]any) []string {
	out := make([]string, 0)
	for key := range root {
		if key != MCPConfigServersKey {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func normalizeMCPServerEntry(path, name string, entry map[string]any) (map[string]any, error) {
	normalized, ok := cloneMCPJSONObject(entry).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s.%s.%s must be an object", path, MCPConfigServersKey, name)
	}
	command, hasCommand, err := mcpStringField(normalized, "command")
	if err != nil {
		return nil, fmt.Errorf("%s.%s.%s.command %s", path, MCPConfigServersKey, name, err)
	}
	url, hasURL, err := mcpStringField(normalized, "url")
	if err != nil {
		return nil, fmt.Errorf("%s.%s.%s.url %s", path, MCPConfigServersKey, name, err)
	}
	if !hasCommand && !hasURL {
		return nil, fmt.Errorf("%s.%s.%s must declare command or url", path, MCPConfigServersKey, name)
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
		return nil, fmt.Errorf("%s.%s.%s.args must be an array of strings", path, MCPConfigServersKey, name)
	}
	if err := validateMCPStringMapField(normalized, "env"); err != nil {
		return nil, fmt.Errorf("%s.%s.%s.env must be an object with string values", path, MCPConfigServersKey, name)
	}
	if err := validateMCPStringMapField(normalized, "headers"); err != nil {
		return nil, fmt.Errorf("%s.%s.%s.headers must be an object with string values", path, MCPConfigServersKey, name)
	}
	if err := validateMCPStringField(normalized, "transport"); err != nil {
		return nil, fmt.Errorf("%s.%s.%s.transport must be a string", path, MCPConfigServersKey, name)
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
	if _, ok := raw.(string); !ok {
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
