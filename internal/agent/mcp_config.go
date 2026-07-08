package agent

import (
	"context"
	"fmt"
	"strings"

	agentruntime "csgclaw/internal/runtime"
	"csgclaw/internal/utils"
)

type MCPConfigView struct {
	AgentID     string         `json:"agent_id"`
	RuntimeKind string         `json:"runtime_kind"`
	Desired     map[string]any `json:"desired"`
	Actual      map[string]any `json:"actual"`
}

func (s *Service) MCPConfigView(ctx context.Context, id string) (MCPConfigView, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return MCPConfigView{}, fmt.Errorf("agent id is required")
	}
	got, ok := s.agentSnapshot(id)
	if !ok {
		return MCPConfigView{}, fmt.Errorf("agent %q not found", id)
	}
	desired := utils.CloneAnyMap(got.MCPConfig)
	actual := utils.CloneAnyMap(got.MCPConfig)

	runtimeKind := strings.TrimSpace(got.RuntimeKind)
	if runtimeKind != "" {
		rt, err := s.runtimeForKind(runtimeKind)
		if err != nil {
			return MCPConfigView{}, err
		}
		if lister, ok := rt.(agentruntime.MCPConfigListController); ok {
			listed, err := lister.ListMCPConfig(ctx, runtimeHandleForAgent(got), mcpConfigSnapshotForAgent(got.MCPConfig))
			if err != nil {
				return MCPConfigView{}, err
			}
			actual = utils.CloneAnyMap(listed.Config)
			if len(actual) == 0 {
				actual = map[string]any{"mcpServers": map[string]any{}}
			}
		}
	}

	return MCPConfigView{
		AgentID:     got.ID,
		RuntimeKind: runtimeKind,
		Desired:     desired,
		Actual:      actual,
	}, nil
}
