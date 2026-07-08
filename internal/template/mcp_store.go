package template

import (
	"context"
	"fmt"

	"csgclaw/internal/config"
	"csgclaw/internal/localstore"
)

type MCPStateStore interface {
	ReadMCPServers(ctx context.Context) (map[string]any, error)
	WriteMCPServers(ctx context.Context, servers map[string]any) error
}

type localMCPStateStore struct {
	statePath func() (string, error)
}

func defaultMCPStateStore() MCPStateStore {
	return localMCPStateStore{statePath: config.DefaultStatePath}
}

func (s localMCPStateStore) ReadMCPServers(context.Context) (map[string]any, error) {
	path, err := s.rootStatePath()
	if err != nil {
		return nil, err
	}
	var servers map[string]any
	ok, err := localstore.ReadSection(path, hubStateMCPServersKey, &servers)
	if err != nil {
		return nil, fmt.Errorf("read hub mcp servers from root state: %w", err)
	}
	if !ok || servers == nil {
		return map[string]any{}, nil
	}
	return servers, nil
}

func (s localMCPStateStore) WriteMCPServers(_ context.Context, servers map[string]any) error {
	path, err := s.rootStatePath()
	if err != nil {
		return err
	}
	if servers == nil {
		servers = map[string]any{}
	}
	if err := localstore.WriteSection(path, hubStateMCPServersKey, servers); err != nil {
		return fmt.Errorf("write hub mcp servers to root state: %w", err)
	}
	return nil
}

func (s localMCPStateStore) rootStatePath() (string, error) {
	if s.statePath != nil {
		return s.statePath()
	}
	return config.DefaultStatePath()
}
