package agents

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"csgclaw/internal/config"
	"csgclaw/internal/resourcequery"
	skillinstall "csgclaw/internal/skill/installstate"
	skilllocal "csgclaw/internal/skill/local"
)

func TestAgentSkillCopiesPreserveInstallationOrigin(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "add"
		if replace {
			name = "replace"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			svc, err := NewController(testModelConfig(), config.ServerConfig{}, "", filepath.Join(t.TempDir(), "agents.json"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = svc.Close() })
			item := Agent{ID: "agent-origin", Name: "origin", Role: RoleWorker, RuntimeKind: RuntimeKindCodex}
			svc.agents[item.ID] = item
			globalRoot, err := skilllocal.SkillsRoot()
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"market": skillinstall.SourceMarket, "upload": skillinstall.SourceLocal, "legacy": skillinstall.SourceUnknown, "skill-creator": skillinstall.SourceBuiltin}
			for _, name := range []string{"market", "upload", "legacy"} {
				dir := filepath.Join(globalRoot, name)
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\ndescription: example\n---\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if name != "legacy" {
					origin := skillinstall.Record{Source: want[name]}
					if name == "market" {
						origin.SourceURL = "https://hub.example.test/skills/owner/market"
					}
					if err := skillinstall.Update(globalRoot, name, &origin, nil); err != nil {
						t.Fatal(err)
					}
				}
			}
			names := []string{"market", "upload", "legacy", "skill-creator"}
			if replace {
				err = svc.ReplaceSkills(context.Background(), item.ID, names)
			} else {
				err = svc.BatchAddSkills(item.ID, names)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := skillinstall.Update(globalRoot, "market", &skillinstall.Record{Source: skillinstall.SourceLocal}, nil); err != nil {
				t.Fatal(err)
			}
			for _, paged := range []bool{false, true} {
				items, err := svc.Workspace().SkillSummaries(context.Background(), item.ID)
				if paged {
					items, _, err = svc.Workspace().SkillSummariesPage(context.Background(), item.ID, resourcequery.Query{Mode: "page", Page: 1, Size: 20})
				}
				if err != nil || len(items) != len(want) {
					t.Fatalf("skill summaries = %+v, error = %v", items, err)
				}
				for _, summary := range items {
					if summary.Source != want[summary.Name] {
						t.Fatalf("skill %s source = %q, want %q", summary.Name, summary.Source, want[summary.Name])
					}
				}
			}
			layout, err := svc.AgentLayout(item.ID)
			if err != nil {
				t.Fatal(err)
			}
			records, err := skillinstall.Read(layout.SkillsRoot)
			if err != nil || records["market"].SourceURL != "https://hub.example.test/skills/owner/market" {
				t.Fatalf("installation origins = %+v, error = %v", records, err)
			}
			if err := svc.DeleteSkill(item.ID, "market"); err != nil {
				t.Fatal(err)
			}
			records, err = skillinstall.Read(layout.SkillsRoot)
			if _, exists := records["market"]; err != nil || exists {
				t.Fatalf("removed skill retained its origin: %+v, error = %v", records, err)
			}
		})
	}
}

func TestAgentSkillCopyFailurePreservesInstallationOrigin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc, err := NewController(testModelConfig(), config.ServerConfig{}, "", filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	item := Agent{ID: "agent-origin", Name: "origin", Role: RoleWorker, RuntimeKind: RuntimeKindCodex}
	svc.agents[item.ID] = item
	globalRoot, err := skilllocal.SkillsRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(globalRoot, "example")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Example"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := skillinstall.Update(globalRoot, "example", &skillinstall.Record{Source: skillinstall.SourceMarket}, nil); err != nil {
		t.Fatal(err)
	}
	layout, err := svc.AgentLayout(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	previous := skillinstall.Record{Source: skillinstall.SourceLocal}
	if err := skillinstall.Update(layout.SkillsRoot, "example", &previous, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.BatchAddSkills(item.ID, []string{"example"}); !errors.Is(err, ErrWorkspaceSymlinkDenied) {
		t.Fatalf("unsafe skill copy error = %v", err)
	}
	records, err := skillinstall.Read(layout.SkillsRoot)
	if err != nil || records["example"].SourceKind() != skillinstall.SourceLocal {
		t.Fatalf("failed copy changed the previous origin: %+v, error = %v", records, err)
	}
	if _, err := os.Stat(filepath.Join(layout.SkillsRoot, "example")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed copy retained its destination: %v", err)
	}
	if err := os.Mkdir(filepath.Join(layout.SkillsRoot, "example"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := svc.BatchAddSkills(item.ID, []string{"example"}); !errors.Is(err, ErrAgentSkillAlreadyExists) {
		t.Fatalf("existing skill copy error = %v", err)
	}
	records, err = skillinstall.Read(layout.SkillsRoot)
	if err != nil || records["example"].SourceKind() != skillinstall.SourceLocal {
		t.Fatalf("rejected copy changed the previous origin: %+v, error = %v", records, err)
	}
}

func TestWorkspaceSkillRestorationPreservesInstallationOrigins(t *testing.T) {
	for _, name := range []string{"commit", "rollback", "recovery"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			svc, err := NewController(testModelConfig(), config.ServerConfig{}, "", filepath.Join(t.TempDir(), "agents.json"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = svc.Close() })
			root, err := svc.agentSkillsRoot("agent-origin", RuntimeKindCodex)
			if err != nil {
				t.Fatal(err)
			}
			for name, source := range map[string]string{"skill-creator": skillinstall.SourceLocal, "custom": skillinstall.SourceMarket} {
				if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, name, "SKILL.md"), []byte("# Original"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := skillinstall.Update(root, name, &skillinstall.Record{Source: source}, nil); err != nil {
					t.Fatal(err)
				}
			}
			recordsPath := filepath.Join(root, ".skillhub-lock.json")
			original, err := os.ReadFile(recordsPath)
			if err != nil {
				t.Fatal(err)
			}
			preserved, err := svc.prepareWorkspaceSkillsPreservation("agent-origin", RuntimeKindCodex, RuntimeKindCodex, RoleWorker)
			if err != nil || preserved == nil {
				t.Fatalf("preserve workspace skills: %v", err)
			}
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			if err := svc.installDefaultSystemSkills("agent-origin", RuntimeKindCodex); err != nil {
				t.Fatal(err)
			}
			generated, err := os.ReadFile(recordsPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := preserved.Restore(); err != nil {
				t.Fatal(err)
			}
			records, err := skillinstall.Read(root)
			if err != nil || records["custom"].SourceKind() != skillinstall.SourceMarket || records["skill-creator"].SourceKind() != skillinstall.SourceBuiltin || records["skill-installer"].SourceKind() != skillinstall.SourceBuiltin {
				t.Fatalf("restored origins = %+v, error = %v", records, err)
			}
			if name != "commit" {
				if name == "recovery" {
					if err := svc.recoverWorkspaceSkillsTransaction("agent-origin"); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := preserved.RevertRestore(); err != nil {
						t.Fatal(err)
					}
					afterRevert, err := os.ReadFile(recordsPath)
					if err != nil || !bytes.Equal(afterRevert, generated) {
						t.Fatalf("regenerated origins were not restored: %s, error = %v", afterRevert, err)
					}
					if err := preserved.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
				afterRollback, err := os.ReadFile(recordsPath)
				if err != nil || !bytes.Equal(afterRollback, original) {
					t.Fatalf("original origins were not restored: %s, error = %v", afterRollback, err)
				}
			} else if err := preserved.Commit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
