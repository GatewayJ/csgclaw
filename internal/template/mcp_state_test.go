package template

import (
	"context"
	"testing"

	"csgclaw/internal/config"
)

func TestMCPServersUsesInjectedStateStore(t *testing.T) {
	store := &memoryMCPStateStore{
		servers: map[string]any{
			"filesystem": map[string]any{
				"command": "npx",
				"args":    []any{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			},
		},
	}
	svc, err := NewService(config.HubConfig{}, func(config.HubRegistryConfig) (Store, error) {
		return stubStore{}, nil
	}, WithMCPStateStore(store))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	listed, err := svc.ListMCPServers(context.Background())
	if err != nil {
		t.Fatalf("ListMCPServers() error = %v", err)
	}
	if _, ok := listed["filesystem"]; !ok {
		t.Fatalf("ListMCPServers() missing filesystem server: %#v", listed)
	}

	if _, err := svc.CreateMCPServer(context.Background(), "github", map[string]any{
		"command": "npx",
		"args":    []any{"-y", "@modelcontextprotocol/server-github"},
	}); err != nil {
		t.Fatalf("CreateMCPServer() error = %v", err)
	}
	if store.writes != 1 {
		t.Fatalf("store writes = %d, want 1", store.writes)
	}
	if _, ok := store.servers["github"]; !ok {
		t.Fatalf("injected store missing github server after write: %#v", store.servers)
	}
}

type memoryMCPStateStore struct {
	servers map[string]any
	writes  int
}

func (s *memoryMCPStateStore) ReadMCPServers(context.Context) (map[string]any, error) {
	return cloneStateMap(s.servers), nil
}

func (s *memoryMCPStateStore) WriteMCPServers(_ context.Context, servers map[string]any) error {
	s.writes++
	s.servers = cloneStateMap(servers)
	return nil
}
