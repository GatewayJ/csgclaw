package codex

import (
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestSanitizeCopiedCodexConfigFeatureTables(t *testing.T) {
	for _, header := range []string{
		`[features.context_management]`,
		`[ "features" . "context_management" ] # desktop options`,
		`['features'.context_management]`,
		`[[features.context_management]]`,
	} {
		t.Run(header, func(t *testing.T) {
			input := "model = \"test-model\"\n[features]\nweb_search = true\n" + header + "\nexperimental_mode = true\n[features.context_management.options]\nvalue = 1\n[memories]\ngenerate_memories = false\n"
			output := sanitizeCopiedCodexConfigContent(input)
			var config struct {
				Model    string          `toml:"model"`
				Features map[string]bool `toml:"features"`
				Memories map[string]bool `toml:"memories"`
			}
			if err := toml.Unmarshal([]byte(output), &config); err != nil {
				t.Fatalf("decode compatible config: %v", err)
			}
			if config.Model != "test-model" || len(config.Features) != 1 || !config.Features["web_search"] {
				t.Fatalf("lost compatible settings: %#v", config)
			}
			if value, ok := config.Memories["generate_memories"]; !ok || value {
				t.Fatalf("lost following table: %#v", config.Memories)
			}
			if strings.Contains(output, "context_management") {
				t.Fatalf("retained incompatible feature table: %s", output)
			}
			if again := sanitizeCopiedCodexConfigContent(output); again != output {
				t.Fatal("sanitization is not idempotent")
			}
		})
	}
}
