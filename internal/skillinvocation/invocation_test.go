package skillinvocation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSlash(t *testing.T) {
	invocation, ok := ParseSlash("  /skill-creator  build a test skill\nwith docs ")
	if !ok {
		t.Fatal("ParseSlash() ok = false, want true")
	}
	if invocation.Slug != "skill-creator" {
		t.Fatalf("Slug = %q, want skill-creator", invocation.Slug)
	}
	if invocation.Instruction != "build a test skill\nwith docs" {
		t.Fatalf("Instruction = %q", invocation.Instruction)
	}
}

func TestParseSlashRejectsNonSkillText(t *testing.T) {
	tests := []string{
		"hello /skill",
		"/",
		"/skills/use task",
		"/bad:slug task",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, ok := ParseSlash(input); ok {
				t.Fatalf("ParseSlash(%q) ok = true, want false", input)
			}
		})
	}
}

func TestBuildMessageLoadsSkillAndSupportingFiles(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "reviewer")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Reviewer\nRead code carefully.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "notes.md"), []byte("notes"), 0o644); err != nil {
		t.Fatalf("WriteFile(supporting file) error = %v", err)
	}

	got, err := BuildMessage(BuildOptions{
		WorkspaceRoot:   root,
		Slug:            "reviewer",
		Instruction:     "check the bug",
		RuntimeSkillDir: "/home/picoclaw/.picoclaw/workspace/skills/reviewer",
	})
	if err != nil {
		t.Fatalf("BuildMessage() error = %v", err)
	}
	for _, want := range []string{
		`invoked the "reviewer" skill`,
		"# Reviewer\nRead code carefully.",
		"[Skill directory: skills/reviewer]",
		"/home/picoclaw/.picoclaw/workspace/skills/reviewer",
		"- references/notes.md",
		"check the bug",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildMessage() missing %q in:\n%s", want, got)
		}
	}
}
