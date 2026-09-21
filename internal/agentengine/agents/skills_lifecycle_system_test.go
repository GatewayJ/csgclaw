package agents

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"testing"

	"csgclaw/internal/config"
	agentruntime "csgclaw/internal/runtime"
	skillsystem "csgclaw/internal/skill/system"
)

func assertDefaultSystemSkills(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"skill-creator", "skill-installer"} {
		source, err := skillsystem.ResolveSource(name)
		if err != nil {
			t.Fatal(err)
		}
		want, err := fs.ReadFile(source.FS, path.Join(source.RootPath, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(root, name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("内建技能 %s 内容与当前版本不同", name)
		}
	}
}

func TestHostWorkerInstallsSystemSkillsBeforeCreateAndRecreate(t *testing.T) {
	for _, kind := range []string{RuntimeKindCodex, RuntimeKindDSH} {
		t.Run(kind, func(t *testing.T) {
			var svc *Controller
			var root string
			provisions, starts := 0, 0
			rt := fakeAgentRuntime{
				kind:      kind,
				provision: func(context.Context, agentruntime.ProvisionRequest) error { provisions++; return nil },
				del:       func(context.Context, agentruntime.Handle) error { return os.RemoveAll(root) },
				new: func(_ context.Context, spec agentruntime.Spec) (agentruntime.Handle, error) {
					var err error
					root, err = svc.agentSkillsRoot(spec.AgentID, kind)
					if err != nil {
						t.Fatal(err)
					}
					assertDefaultSystemSkills(t, root)
					starts++
					return agentruntime.Handle{RuntimeID: spec.RuntimeID, HandleID: "test-session"}, nil
				},
				info: func(_ context.Context, h agentruntime.Handle) (agentruntime.Info, error) {
					return agentruntime.Info{HandleID: h.HandleID, State: agentruntime.StateRunning}, nil
				},
			}
			var err error
			svc, err = NewController(testModelConfig(), config.ServerConfig{}, "", filepath.Join(t.TempDir(), "agents.json"), WithRuntime(rt))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = svc.Close() })
			item, err := svc.CreateWorker(context.Background(), CreateAgentSpec{
				ID: "agent-alice", Name: "alice", RuntimeKind: kind,
				AgentProfile: AgentProfile{Name: "alice", Provider: ProviderAPI, BaseURL: "https://api.example/v1", APIKey: "test", ModelID: "test", ProfileComplete: true},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "custom"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "custom", "SKILL.md"), []byte("custom skill"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "skill-creator", "SKILL.md"), []byte("old version"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(root, "skill-installer")); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.RecreateRecord(context.Background(), item.ID); err != nil {
				t.Fatal(err)
			}
			assertDefaultSystemSkills(t, root)
			data, err := os.ReadFile(filepath.Join(root, "custom", "SKILL.md"))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "custom skill" {
				t.Fatal("自定义技能内容发生变化")
			}
			if provisions != 2 || starts != 2 {
				t.Fatalf("创建和重建调用次数：Provision=%d，New=%d", provisions, starts)
			}
		})
	}
}
