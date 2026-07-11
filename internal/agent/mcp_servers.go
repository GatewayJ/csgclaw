package agent

import (
	"context"
	"fmt"
	"strings"

	agentruntime "csgclaw/internal/runtime"
	"csgclaw/internal/utils"
)

type MCPServersView struct {
	AgentID     string         `json:"agent_id"`
	RuntimeKind string         `json:"runtime_kind"`
	Desired     map[string]any `json:"desired"`
	Actual      map[string]any `json:"actual"`
	ActualError string         `json:"actual_error,omitempty"`
}

// cloneMCPServers preserves the distinction between no managed MCP state and
// an explicitly managed empty server map. Generic map helpers intentionally
// collapse empty maps, which would turn a user-initiated clear into an
// unmanaged state after a save or reload.
func cloneMCPServers(servers map[string]any) map[string]any {
	if servers == nil {
		return nil
	}
	if len(servers) == 0 {
		return map[string]any{}
	}
	return utils.CloneAnyMap(servers)
}

func (s *Service) MCPServersView(ctx context.Context, id string) (MCPServersView, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return MCPServersView{}, fmt.Errorf("agent id is required")
	}
	got, ok := s.agentSnapshot(id)
	if !ok {
		return MCPServersView{}, fmt.Errorf("agent %q not found", id)
	}
	runtimeKind := strings.TrimSpace(got.RuntimeKind)
	view := MCPServersView{
		AgentID:     got.ID,
		RuntimeKind: runtimeKind,
		Desired:     cloneMCPServers(got.MCPServers),
	}
	if runtimeKind == "" {
		return view, nil
	}

	rt, err := s.runtimeForKind(runtimeKind)
	if err != nil {
		view.ActualError = fmt.Sprintf("read runtime MCP servers: %v", err)
		return view, nil
	}
	lister, ok := rt.(agentruntime.MCPServersListController)
	if !ok {
		view.ActualError = fmt.Sprintf("runtime_kind %q does not expose MCP server state", runtimeKind)
		return view, nil
	}
	listed, err := lister.ListMCPServers(ctx, runtimeHandleForAgent(got), mcpServersSnapshotForAgent(got.MCPServers))
	if err != nil {
		view.ActualError = fmt.Sprintf("read runtime MCP servers: %v", err)
		return view, nil
	}
	view.Actual = cloneMCPServers(listed.Servers)
	return view, nil
}
