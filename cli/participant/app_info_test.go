package participant

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"csgclaw/internal/apitypes"
	"csgclaw/internal/participant"
)

func TestRenderExecProviderAppInfo(t *testing.T) {
	var out bytes.Buffer
	err := renderExecProviderAppInfo(
		strings.NewReader(`{"protocolVersion":1,"provider":"csgclaw-pt","ids":["app_secret","app_id","missing"]}`),
		&out,
		apitypes.FeishuBotAppInfo{
			AppID:     "cli_dev",
			AppSecret: "dev-secret",
		},
	)
	if err != nil {
		t.Fatalf("renderExecProviderAppInfo() error = %v", err)
	}
	var got execProviderResponse
	if err := json.NewDecoder(&out).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ProtocolVersion != 1 {
		t.Fatalf("protocolVersion = %d, want 1", got.ProtocolVersion)
	}
	if got.Values["app_secret"] != "dev-secret" {
		t.Fatalf("app_secret = %q, want dev-secret", got.Values["app_secret"])
	}
	if got.Values["app_id"] != "cli_dev" {
		t.Fatalf("app_id = %q, want cli_dev", got.Values["app_id"])
	}
	if got.Errors["missing"].Message == "" {
		t.Fatalf("missing id error was not reported: %#v", got.Errors)
	}
}

func TestRenderAppInfoRedactsSecret(t *testing.T) {
	var out bytes.Buffer
	if err := renderAppInfo("json", &out, apitypes.FeishuBotAppInfo{
		AgentID:       "u-dev",
		ParticipantID: "pt-dev",
		AppID:         "cli_dev",
		AppSecret:     "dev-secret",
	}); err != nil {
		t.Fatalf("renderAppInfo() error = %v", err)
	}
	if strings.Contains(out.String(), "dev-secret") {
		t.Fatalf("renderAppInfo leaked secret: %s", out.String())
	}
	if !strings.Contains(out.String(), participant.RedactedSecretValue) {
		t.Fatalf("renderAppInfo did not include redaction marker: %s", out.String())
	}
}
