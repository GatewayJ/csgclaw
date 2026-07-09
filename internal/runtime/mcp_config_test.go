package runtime

import (
	"math"
	"strings"
	"testing"
)

func TestNormalizeMCPConfigAcceptsTimeoutFields(t *testing.T) {
	got, err := NormalizeMCPConfig(map[string]any{
		"mcpServers": map[string]any{
			"grafana": map[string]any{
				"command":             "uvx",
				"args":                []any{"mcp-grafana"},
				"startup_timeout_sec": float64(90),
				"tool_timeout_sec":    120,
			},
		},
	})
	if err != nil {
		t.Fatalf("NormalizeMCPConfig() error = %v", err)
	}
	servers := got["mcpServers"].(map[string]any)
	grafana := servers["grafana"].(map[string]any)
	if got, want := grafana["startup_timeout_sec"], float64(90); got != want {
		t.Fatalf("startup_timeout_sec = %#v, want %#v", got, want)
	}
	if got, want := grafana["tool_timeout_sec"], 120; got != want {
		t.Fatalf("tool_timeout_sec = %#v, want %#v", got, want)
	}
}

func TestNormalizeMCPConfigRejectsInvalidTimeoutField(t *testing.T) {
	for _, value := range []any{30.5, float64(math.MaxInt64)} {
		_, err := NormalizeMCPConfig(map[string]any{
			"mcpServers": map[string]any{
				"grafana": map[string]any{
					"command":             "uvx",
					"startup_timeout_sec": value,
				},
			},
		})
		if err == nil {
			t.Fatalf("NormalizeMCPConfig(startup_timeout_sec=%#v) error = nil, want error", value)
		}
		if !strings.Contains(err.Error(), "startup_timeout_sec must be a positive integer") {
			t.Fatalf("NormalizeMCPConfig(startup_timeout_sec=%#v) error = %v", value, err)
		}
	}
}
