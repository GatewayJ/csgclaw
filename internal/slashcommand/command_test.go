package slashcommand

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCanonicalSlashCommandPrefix(t *testing.T) {
	cmd, ok, err := Parse(`<slash-command name="use-skill" arg="skill-creator"></slash-command> create a review skill`)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !ok {
		t.Fatal("Parse() ok = false, want true")
	}
	if cmd.Name != "use-skill" || cmd.Arg != "skill-creator" || cmd.Body != "create a review skill" {
		t.Fatalf("command = %+v, want use-skill skill-creator prompt body", cmd)
	}
}

func TestParseCanonicalSlashCommandSelfClosingPrefix(t *testing.T) {
	cmd, ok, err := Parse(`<slash-command name="use-skill" arg="skill-creator"/> create a review skill`)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !ok {
		t.Fatal("Parse() ok = false, want true")
	}
	if cmd.Body != "create a review skill" {
		t.Fatalf("Body = %q, want trailing prompt", cmd.Body)
	}
}

func TestNormalizeCanonicalSlashCommandPrefix(t *testing.T) {
	got, ok, err := Normalize(`  <slash-command arg="skill-creator" name="use-skill"/>  create & review <safely>  `)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if !ok {
		t.Fatal("Normalize() ok = false, want true")
	}
	want := `<slash-command name="use-skill" arg="skill-creator"></slash-command> create & review <safely>`
	if got != want {
		t.Fatalf("Normalize() = %q, want %q", got, want)
	}
}

func TestRenderKeepsUserPromptOutsideCommandElement(t *testing.T) {
	got, err := Render(Command{Name: "use-skill", Arg: "skill-creator", Body: `1 < 2 & "quote"`})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	want := `<slash-command name="use-skill" arg="skill-creator"></slash-command> 1 < 2 & "quote"`
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestParseRejectsLegacySlashText(t *testing.T) {
	if _, ok, err := Parse(`/skill-creator create a review skill`); ok || err != nil {
		t.Fatalf("Parse(legacy slash) = ok %v err %v, want ok false err nil", ok, err)
	}
}

func TestNormalizeFeishuInputConvertsSlashSkillShorthand(t *testing.T) {
	got, ok, err := NormalizeFeishuInput(`/skill-creator create a review skill`)
	if err != nil {
		t.Fatalf("NormalizeFeishuInput() error = %v", err)
	}
	if !ok {
		t.Fatal("NormalizeFeishuInput() ok = false, want true")
	}
	want := `<slash-command name="use-skill" arg="skill-creator"></slash-command> create a review skill`
	if got != want {
		t.Fatalf("NormalizeFeishuInput() = %q, want %q", got, want)
	}
}

func TestRenderFeishuFallbackConvertsCanonicalUseSkillToSlashText(t *testing.T) {
	got := RenderFeishuFallback(`<slash-command name="use-skill" arg="skill-creator"></slash-command> create a review skill`)
	want := `/skill-creator create a review skill`
	if got != want {
		t.Fatalf("RenderFeishuFallback() = %q, want %q", got, want)
	}
}

func TestNormalizeRejectsMalformedSlashCommand(t *testing.T) {
	for _, input := range []string{
		`<slash-command name=""></slash-command> body`,
		`<slash-command/> body`,
	} {
		if got, ok, err := Normalize(input); err == nil || ok || got != "" {
			t.Fatalf("Normalize(%q) = %q ok %v err %v, want error", input, got, ok, err)
		}
	}
}

func TestParseRejectsNestedSlashCommandElement(t *testing.T) {
	_, ok, err := Parse(`<slash-command name="use-skill" arg="skill-creator"><b>bad</b></slash-command> prompt`)
	if err == nil || ok {
		t.Fatalf("Parse(nested) ok=%v err=%v, want nested element error", ok, err)
	}
}

func TestParseRejectsPromptInsideSlashCommandElement(t *testing.T) {
	_, ok, err := Parse(`<slash-command name="use-skill" arg="skill-creator">prompt</slash-command>`)
	if err == nil || ok {
		t.Fatalf("Parse(inline prompt) ok=%v err=%v, want prompt outside command element error", ok, err)
	}
}

func TestExpandUseSkillPromptLoadsSkillFile(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "reviewer")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Reviewer\nRead code carefully.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error = %v", err)
	}

	got, ok, err := ExpandUseSkillPrompt(
		`<slash-command name="use-skill" arg="reviewer"></slash-command> check this bug`,
		root,
	)
	if err != nil {
		t.Fatalf("ExpandUseSkillPrompt() error = %v", err)
	}
	if !ok {
		t.Fatal("ExpandUseSkillPrompt() ok = false, want true")
	}
	for _, want := range []string{
		`invoked the "reviewer" skill`,
		"# Reviewer\nRead code carefully.",
		"[Skill directory: skills/reviewer]",
		"check this bug",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ExpandUseSkillPrompt() missing %q in:\n%s", want, got)
		}
	}
}
