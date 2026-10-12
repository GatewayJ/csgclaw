package larkcli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWaitLoginPreservesCompletedAuthorizationWithMissingScopes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		data      string
		wantError bool
	}{
		{"partial_authorization", `{"event":"authorization_complete","user_open_id":"ou_user","warning":{"type":"missing_scope"},"granted":["docs:read"],"missing":["mail:read"]}`, false},
		{"permission_failure", `{"error":{"type":"missing_scope"}}`, true},
		{"missing_identity", `{"event":"authorization_complete","warning":{"type":"missing_scope"}}`, true},
		{"storage_failure", `{"event":"authorization_complete","user_open_id":"ou_user","warning":{"type":"storage_error"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := Client{Runner: func(context.Context, Client, io.Reader, ...string) ([]byte, error) {
				return []byte(tc.data), &OperationError{Code: "cli_operation_failed"}
			}}
			if err := client.WaitLogin(context.Background(), "device"); (err != nil) != tc.wantError {
				t.Fatalf("WaitLogin() error = %v", err)
			}
		})
	}
}

func TestWaitLoginReadsCompletedAuthorizationFromNonzeroCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	root := t.TempDir()
	executable := filepath.Join(root, "lark-cli")
	script := `#!/bin/sh
printf '%s\n' '{"event":"authorization_complete","user_open_id":"ou_user","warning":{"type":"missing_scope"}}'
exit 3
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	client := Client{Executable: executable, ConfigDir: root}
	if err := client.WaitLogin(context.Background(), "device"); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizationURLAndErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		value   string
		allowed bool
	}{{"https://open.feishu.cn/device?token=test", true}, {"https://accounts.larksuite.com/device", true}, {"https://feishu.cn.attacker.example/device", false}, {"https://user@open.feishu.cn/device", false}, {"http://open.feishu.cn/device", false}, {"https://open.feishu.cn:8443/device", false}} {
		if got := AllowedAuthorizationURL(tc.value); got != tc.allowed {
			t.Errorf("%q allowed=%v", tc.value, got)
		}
	}
	var result *OperationError
	if err := operationError([]byte(`{"error":"access_denied","access_token":"secret"}`)); !errors.As(err, &result) || result.Code != "authorization_denied" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error=%v", err)
	}
}
func TestSelectedMigrationConfigRetainsCredentialReferencesAndIdentityPolicy(t *testing.T) {
	input := []byte(`{"currentApp":"selected","strictMode":true,"apps":[{"name":"other","appId":"cli_other"},{"name":"selected","appId":"cli_selected","appSecret":{"source":"keychain"},"users":[{"openId":"ou_existing","scope":"docs:read"}],"defaultAs":"auto"}]}`)
	data, identity, err := SelectConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	if identity.AppID != "cli_selected" {
		t.Fatalf("identity=%+v", identity)
	}
	var selected struct {
		StrictMode bool                         `json:"strictMode"`
		Apps       []map[string]json.RawMessage `json:"apps"`
	}
	if err := json.Unmarshal(data, &selected); err != nil {
		t.Fatal(err)
	}
	if !selected.StrictMode || len(selected.Apps) != 1 || !strings.Contains(string(selected.Apps[0]["users"]), "ou_existing") || !strings.Contains(string(selected.Apps[0]["appSecret"]), "keychain") {
		t.Fatalf("selected=%s", data)
	}
}
func TestManagementEnvironmentUsesOnlyResourceConfiguration(t *testing.T) {
	t.Setenv("LARKSUITE_CLI_CONFIG_DIR", "host-config")
	t.Setenv("LARK_CHANNEL_CONFIG", "channel-config")
	t.Setenv("LARKSUITE_CLI_USER_ACCESS_TOKEN", "host-token")
	t.Setenv("OPENCLAW_HOME", "host-openclaw")
	environment := (Client{ConfigDir: "resource-config", OperationID: "operation"}).Environment()
	values := strings.Join(environment, "\n")
	for _, forbidden := range []string{"host-config", "channel-config", "host-token", "host-openclaw"} {
		if strings.Contains(values, forbidden) {
			t.Fatalf("inherited configuration: %s", forbidden)
		}
	}
	if !strings.Contains(values, "LARKSUITE_CLI_CONFIG_DIR=resource-config") {
		t.Fatal("resource configuration missing")
	}
}
