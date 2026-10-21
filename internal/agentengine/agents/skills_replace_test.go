package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"csgclaw/internal/agentengine/contract"
	"csgclaw/internal/config"
	agentruntime "csgclaw/internal/runtime"
	skillinstall "csgclaw/internal/skill/installstate"
	skilllocal "csgclaw/internal/skill/local"
	skill "csgclaw/internal/skill/state"
)

func TestReplaceSelectedSkillsPreparesConsistentSnapshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc, err := NewController(testModelConfig(), config.ServerConfig{}, "", filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	item := Agent{ID: "agent-snapshot", Name: "snapshot", Role: RoleWorker, RuntimeKind: RuntimeKindCodex}
	svc.agents[item.ID] = item
	globalRoot, err := skilllocal.SkillsRoot()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(globalRoot, "selected")
	other := filepath.Join(filepath.Dir(globalRoot), "other-skill")
	holding := filepath.Join(filepath.Dir(globalRoot), "holding-skill")
	for root, version := range map[string]string{target: "A", other: "B"} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# "+version+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 20; n++ {
			if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%02d.txt", n)), []byte(strings.Repeat(version, 8192)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := skillinstall.Update(globalRoot, "selected", &skillinstall.Record{Version: "A"}, nil); err != nil {
		t.Fatal(err)
	}
	done, started := make(chan struct{}), make(chan struct{})
	writerErr := make(chan error, 1)
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		close(started)
		version := "B"
		for {
			select {
			case <-done:
				return
			default:
			}
			err := skillinstall.Update(globalRoot, "selected", &skillinstall.Record{Version: version}, func() error {
				if err := os.Rename(target, holding); err != nil {
					return err
				}
				if err := os.Rename(other, target); err != nil {
					return err
				}
				return os.Rename(holding, other)
			})
			if err != nil {
				writerErr <- err
				return
			}
			if version == "B" {
				version = "A"
			} else {
				version = "B"
			}
		}
	}()
	defer func() {
		close(done)
		writer.Wait()
		select {
		case err := <-writerErr:
			t.Error(err)
		default:
		}
	}()
	<-started
	for iteration := 0; iteration < 30; iteration++ {
		prepared, err := svc.prepareSkillReplacement(item, []string{"selected"})
		if err != nil {
			t.Fatal(err)
		}
		version := prepared.origins["selected"].Version
		for n := 0; n < 20; n++ {
			data, err := os.ReadFile(filepath.Join(prepared.stageRoot, "next", "selected", fmt.Sprintf("file-%02d.txt", n)))
			if err != nil || string(data) != strings.Repeat(version, 8192) {
				prepared.cleanup()
				t.Fatalf("snapshot contains mixed files at iteration %d, file %d, origin %q: %v", iteration, n, version, err)
			}
		}
		prepared.cleanup()
	}
}

func writeReplacementSkill(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceSelectedSkillsUpdatesContentAndRuntime(t *testing.T) {
	for _, tc := range []struct {
		name     string
		disabled bool
		running  bool
		fail     bool
	}{
		{name: "enabled_running", running: true},
		{name: "disabled_running", running: true, disabled: true},
		{name: "disabled_stopped", disabled: true},
		{name: "restart_failure", running: true, disabled: true, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			state, desired := agentruntime.StateStopped, DesiredStateStopped
			if tc.running {
				state, desired = agentruntime.StateRunning, DesiredStateRunning
			}
			var root string
			stops, starts := 0, 0
			startErr := errors.New("replacement restart failed")
			rt := &skillStateRuntime{fakeAgentRuntime: fakeAgentRuntime{kind: RuntimeKindCodex,
				stop: func(context.Context, agentruntime.Handle) (agentruntime.State, error) {
					stops++
					state = agentruntime.StateStopped
					return state, nil
				},
				start: func(context.Context, agentruntime.Handle) (agentruntime.State, error) {
					starts++
					data, err := os.ReadFile(filepath.Join(root, "selected", "SKILL.md"))
					if err != nil || string(data) != "# Updated\n" {
						t.Fatalf("content before restart = %q, error = %v", data, err)
					}
					if tc.fail {
						return state, startErr
					}
					state = agentruntime.StateRunning
					return state, nil
				},
				info: func(_ context.Context, h agentruntime.Handle) (agentruntime.Info, error) {
					return agentruntime.Info{HandleID: h.HandleID, State: state}, nil
				},
			}}
			svc, err := NewController(testModelConfig(), config.ServerConfig{}, "", filepath.Join(t.TempDir(), "agents.json"), WithRuntime(rt))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = svc.Close() })
			item := Agent{ID: "agent-replace", Name: "replace", Role: RoleWorker, RuntimeID: "rt-replace", RuntimeKind: RuntimeKindCodex, RuntimeName: RuntimeNameCodex, Status: string(state), DesiredState: desired, ProfileComplete: true, AgentProfile: AgentProfile{Provider: ProviderAPI, BaseURL: "https://example.test", ModelID: "test", APIKey: "test", ProfileComplete: true}, SkillStates: map[string]skill.State{"local-only": {Enabled: false}}}
			if tc.disabled {
				item.SkillStates["selected"] = skill.State{Enabled: false}
			}
			svc.agents[item.ID] = item
			root, err = svc.agentSkillsRoot(item.ID, item.RuntimeKind)
			if err != nil {
				t.Fatal(err)
			}
			globalRoot, err := skilllocal.SkillsRoot()
			if err != nil {
				t.Fatal(err)
			}
			writeReplacementSkill(t, root, "selected", "# Previous\n")
			writeReplacementSkill(t, root, "local-only", "# Local edits\n")
			writeReplacementSkill(t, globalRoot, "selected", "# Updated\n")
			if err := os.WriteFile(filepath.Join(root, "selected", "stale.txt"), []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := skillinstall.Update(root, "local-only", &skillinstall.Record{Source: skillinstall.SourceLocal}, nil); err != nil {
				t.Fatal(err)
			}
			if err := skillinstall.Update(globalRoot, "selected", &skillinstall.Record{Source: skillinstall.SourceMarket, SourceURL: "https://example.test/new"}, nil); err != nil {
				t.Fatal(err)
			}
			before, err := svc.Get(context.Background(), item.ID, contract.AgentGetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = svc.Update(context.Background(), item.ID, contract.AgentUpdateRequest{Spec: before.Spec, FieldMask: []string{"skills"}, ResourceVersion: before.ResourceVersion, ReplaceSkills: []string{"selected"}})
			if tc.fail {
				if !errors.Is(err, startErr) {
					t.Fatalf("restart error = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tc.running && (starts != 1 || stops != 1) || !tc.running && (starts != 0 || stops != 0) {
				t.Fatalf("runtime stop/start calls = %d/%d", stops, starts)
			}
			if err := svc.Reload(); err != nil {
				t.Fatal(err)
			}
			after, err := svc.Get(context.Background(), item.ID, contract.AgentGetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if after.ResourceVersion == before.ResourceVersion || !skill.Enabled(after.Spec.SkillStates, "selected") || skill.Enabled(after.Spec.SkillStates, "local-only") {
				t.Fatalf("replacement version or enablement not persisted: %+v", after)
			}
			for name, want := range map[string]string{"selected": "# Updated\n", "local-only": "# Local edits\n"} {
				data, err := os.ReadFile(filepath.Join(root, name, "SKILL.md"))
				if err != nil || string(data) != want {
					t.Fatalf("%s content = %q, error = %v", name, data, err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "selected", "stale.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stale file retained: %v", err)
			}
			origins, err := skillinstall.Read(root)
			if err != nil || origins["selected"].SourceURL != "https://example.test/new" || origins["local-only"].Source != skillinstall.SourceLocal {
				t.Fatalf("replacement origins = %+v, error = %v", origins, err)
			}
			if tc.fail {
				stored, _ := svc.Agent(item.ID)
				if !stored.AgentProfile.EnvRestartRequired {
					t.Fatal("restart failure did not retain the pending resource update")
				}
				tc.fail = false
				if _, err := svc.Update(context.Background(), item.ID, contract.AgentUpdateRequest{Spec: after.Spec, FieldMask: []string{"skills"}, ResourceVersion: after.ResourceVersion, ReplaceSkills: []string{"selected"}}); err != nil {
					t.Fatalf("replacement retry failed: %v", err)
				}
			}
		})
	}
}

func TestReplaceSelectedSkillsFailurePreservesFilesAndState(t *testing.T) {
	for _, mode := range []string{"source_missing", "source_symlink", "invalid_origins", "worker_origins_failure", "activation_failure", "duplicate", "not_desired"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			svc, err := NewController(testModelConfig(), config.ServerConfig{}, "", filepath.Join(t.TempDir(), "agents.json"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = svc.Close() })
			item := Agent{ID: "agent-replace", Name: "replace", Role: RoleWorker, RuntimeKind: RuntimeKindCodex, DesiredState: DesiredStateStopped, Status: string(agentruntime.StateStopped), SkillStates: map[string]skill.State{"first": {Enabled: false}}}
			svc.agents[item.ID] = item
			root, err := svc.agentSkillsRoot(item.ID, item.RuntimeKind)
			if err != nil {
				t.Fatal(err)
			}
			globalRoot, err := skilllocal.SkillsRoot()
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"first", "second"} {
				writeReplacementSkill(t, root, name, "# Previous\n")
				writeReplacementSkill(t, globalRoot, name, "# Updated\n")
				if err := skillinstall.Update(root, name, &skillinstall.Record{Source: skillinstall.SourceLocal}, nil); err != nil {
					t.Fatal(err)
				}
			}
			names := []string{"first", "second"}
			switch mode {
			case "source_missing":
				if err := os.RemoveAll(filepath.Join(globalRoot, "second")); err != nil {
					t.Fatal(err)
				}
			case "source_symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(globalRoot, "second", "linked")); err != nil {
					t.Fatal(err)
				}
			case "invalid_origins":
				if err := os.WriteFile(filepath.Join(globalRoot, ".skillhub-lock.json"), []byte("invalid"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "activation_failure":
				if err := os.RemoveAll(filepath.Join(root, "second")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "second"), []byte("blocked destination"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				names = []string{"first", "first"}
			case "not_desired":
				names = []string{"absent"}
			}
			originsBefore, err := skillinstall.Read(root)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "worker_origins_failure" {
				if err := os.Rename(filepath.Join(root, ".skillhub-lock.json"), filepath.Join(root, "saved-origins")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, ".skillhub-lock.json"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			before, err := svc.Get(context.Background(), item.ID, contract.AgentGetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			spec := before.Spec
			if mode == "activation_failure" {
				spec.Skills = append(spec.Skills, "second")
			}
			_, err = svc.Update(context.Background(), item.ID, contract.AgentUpdateRequest{Spec: spec, FieldMask: []string{"skills"}, ResourceVersion: before.ResourceVersion, ReplaceSkills: names})
			if err == nil {
				t.Fatal("replacement unexpectedly succeeded")
			}
			after, err := svc.Get(context.Background(), item.ID, contract.AgentGetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if after.ResourceVersion != before.ResourceVersion || !reflect.DeepEqual(after.Spec.SkillStates, before.Spec.SkillStates) {
				t.Fatalf("failed replacement changed state: before=%+v after=%+v", before, after)
			}
			data, err := os.ReadFile(filepath.Join(root, "first", "SKILL.md"))
			if err != nil || string(data) != "# Previous\n" {
				t.Fatalf("previous content = %q, error = %v", data, err)
			}
			if mode == "worker_origins_failure" {
				if err := os.Remove(filepath.Join(root, ".skillhub-lock.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(root, "saved-origins"), filepath.Join(root, ".skillhub-lock.json")); err != nil {
					t.Fatal(err)
				}
			}
			originsAfter, err := skillinstall.Read(root)
			if err != nil || !reflect.DeepEqual(originsBefore, originsAfter) {
				t.Fatalf("previous origins changed: %+v, error = %v", originsAfter, err)
			}
			if mode == "activation_failure" || mode == "worker_origins_failure" {
				if err := svc.Reload(); err != nil {
					t.Fatal(err)
				}
				stored, _ := svc.Agent(item.ID)
				if skill.Enabled(stored.SkillStates, "first") {
					t.Fatal("failed replacement persisted enabled state")
				}
			}
		})
	}
}
