package template

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	agentruntime "csgclaw/internal/runtime"
)

const hubStateMCPServersKey = "mcpServers"

var (
	ErrHubMCPServerExists   = errors.New("hub mcp server already exists")
	ErrHubMCPServerNotFound = errors.New("hub mcp server not found")
)

var hubStateDocumentMu sync.Mutex

func (s *Service) ListMCPServers(ctx context.Context) (map[string]any, error) {
	hubStateDocumentMu.Lock()
	defer hubStateDocumentMu.Unlock()

	servers, err := s.readMCPServersSection(ctx)
	if err != nil {
		return nil, err
	}
	return cloneStateMap(servers), nil
}

func (s *Service) CreateMCPServer(ctx context.Context, name string, config map[string]any) (map[string]any, error) {
	name, config, err := normalizeMCPServerInput(name, config)
	if err != nil {
		return nil, err
	}
	return s.updateStateDocument(ctx, func(state map[string]any) error {
		if _, exists := state[name]; exists {
			return fmt.Errorf("%w: %s", ErrHubMCPServerExists, name)
		}
		state[name] = config
		return nil
	})
}

func (s *Service) UpdateMCPServer(ctx context.Context, currentName, nextName string, config map[string]any) (map[string]any, error) {
	currentName = strings.TrimSpace(currentName)
	nextName, config, err := normalizeMCPServerInput(nextName, config)
	if err != nil {
		return nil, err
	}
	if currentName == "" {
		return nil, fmt.Errorf("mcp server name is required")
	}
	return s.updateStateDocument(ctx, func(state map[string]any) error {
		if _, exists := state[currentName]; !exists {
			return fmt.Errorf("%w: %s", ErrHubMCPServerNotFound, currentName)
		}
		if nextName != currentName {
			if _, exists := state[nextName]; exists {
				return fmt.Errorf("%w: %s", ErrHubMCPServerExists, nextName)
			}
			delete(state, currentName)
		}
		state[nextName] = config
		return nil
	})
}

func (s *Service) DeleteMCPServer(ctx context.Context, name string) (map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("mcp server name is required")
	}
	return s.updateStateDocument(ctx, func(state map[string]any) error {
		if _, exists := state[name]; !exists {
			return fmt.Errorf("%w: %s", ErrHubMCPServerNotFound, name)
		}
		delete(state, name)
		return nil
	})
}

func (s *Service) readMCPServersSection(ctx context.Context) (map[string]any, error) {
	return s.mcpStateStore().ReadMCPServers(ctx)
}

func (s *Service) writeMCPServersSection(ctx context.Context, servers map[string]any) error {
	return s.mcpStateStore().WriteMCPServers(ctx, servers)
}

func (s *Service) mcpStateStore() MCPStateStore {
	if s == nil || s.mcpStore == nil {
		return defaultMCPStateStore()
	}
	return s.mcpStore
}

func (s *Service) updateStateDocument(ctx context.Context, update func(map[string]any) error) (map[string]any, error) {
	hubStateDocumentMu.Lock()
	defer hubStateDocumentMu.Unlock()

	servers, err := s.readMCPServersSection(ctx)
	if err != nil {
		return nil, err
	}
	if err := update(servers); err != nil {
		return nil, err
	}
	if err := s.writeMCPServersSection(ctx, servers); err != nil {
		return nil, err
	}
	return map[string]any{hubStateMCPServersKey: cloneStateMap(servers)}, nil
}

func normalizeMCPServerInput(name string, config map[string]any) (string, map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, fmt.Errorf("mcp server name is required")
	}
	if config == nil {
		return "", nil, fmt.Errorf("mcp server config is required")
	}
	var err error
	config, err = unwrapMCPServerConfig(name, config)
	if err != nil {
		return "", nil, err
	}
	normalized, err := agentruntime.NormalizeMCPConfig(map[string]any{
		hubStateMCPServersKey: map[string]any{name: config},
	})
	if err != nil {
		return "", nil, err
	}
	servers, err := mcpServersFromState(normalized)
	if err != nil {
		return "", nil, err
	}
	serverConfig, ok := servers[name].(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("mcp server config for %q must be an object", name)
	}
	return name, cloneStateMap(serverConfig), nil
}

func unwrapMCPServerConfig(name string, config map[string]any) (map[string]any, error) {
	rawServers, ok := config[hubStateMCPServersKey]
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

func mcpServersFromState(state map[string]any) (map[string]any, error) {
	rawServers, ok := state[hubStateMCPServersKey]
	if !ok || rawServers == nil {
		return map[string]any{}, nil
	}
	servers, ok := rawServers.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("hub state mcpServers must be an object")
	}
	return servers, nil
}

func cloneStateMap(value map[string]any) map[string]any {
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
