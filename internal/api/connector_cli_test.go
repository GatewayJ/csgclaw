package api

import (
	"context"
	"testing"

	"csgclaw/internal/agentengine"
	agent "csgclaw/internal/agentengine/agents"
	agentruntime "csgclaw/internal/runtime"
)

func TestConnectorCLIVerifyDisabledConfiguration(t *testing.T) {
	for _, status := range []string{"stopped", "running"} {
		t.Run(status, func(t *testing.T) {
			svc := mustNewSeededServiceWithOptions(t, []agent.Agent{{ID: "worker", Name: "worker", Role: agent.RoleWorker, RuntimeKind: agent.RuntimeKindCodex, Status: status}}, agent.WithRuntime(fakeCompatRuntime{kind: agent.RuntimeKindCodex, info: func(context.Context, agentruntime.Handle) (agentruntime.Info, error) {
				return agentruntime.Info{State: agentruntime.State(status)}, nil
			}}))
			runtime := connectorRuntime{h: &Handler{svc: svc, agentEngine: agentengine.New(svc)}}
			loaded, err := runtime.Verify(context.Background(), "worker")
			if err != nil {
				t.Fatal(err)
			}
			if loaded != (status == "running") {
				t.Fatalf("loaded=%v for %s worker", loaded, status)
			}
		})
	}
}

func TestLegacyWorkerRunningUsesDesiredState(t *testing.T) {
	for _, tt := range []struct {
		name, desired, observed string
		running                 bool
	}{
		{"service restart", agent.DesiredStateRunning, "stopped", true},
		{"explicit stop", agent.DesiredStateStopped, "running", false},
		{"legacy running", "", "running", true},
		{"legacy stopped", "", "stopped", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := legacyWorkerRunning(agent.Agent{DesiredState: tt.desired, Status: tt.observed}); got != tt.running {
				t.Fatalf("running=%v, want %v", got, tt.running)
			}
		})
	}
}
