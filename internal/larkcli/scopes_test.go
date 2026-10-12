package larkcli

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestInitUsesExplicitApplicationPermissions(t *testing.T) {
	var displayed string
	client := Client{
		URL: func(value string) { displayed = value },
		Runner: func(_ context.Context, client Client, _ io.Reader, args ...string) ([]byte, error) {
			if !slices.Equal(args, []string{"config", "init", "--new", "--brand", "feishu", "--lang", "zh"}) {
				t.Fatalf("unexpected command: %v", args)
			}
			client.URL("https://open.feishu.cn/page/cli?user_code=test&from=cli&addons=old")
			return nil, nil
		},
	}
	if err := client.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(displayed)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("user_code") != "test" || u.Query().Get("from") != "sdk" || u.Query().Get("tp") != "sdk" || u.Path != "/page/launcher" {
		t.Fatal("registration parameters changed")
	}
	compressed, err := base64.RawURLEncoding.DecodeString(u.Query().Get("addons"))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var addons struct {
		Preset *bool               `json:"preset"`
		Scopes authorizationScopes `json:"scopes"`
	}
	if err := json.NewDecoder(reader).Decode(&addons); err != nil {
		t.Fatal(err)
	}
	if addons.Preset == nil || *addons.Preset {
		t.Fatal("registration must explicitly select the minimal template")
	}
	if !slices.Equal(addons.Scopes.Tenant, autoApproveScopes.Tenant) || !slices.Equal(addons.Scopes.User, autoApproveScopes.User) {
		t.Fatal("registration permissions differ from the reviewed policy")
	}
}

func TestLoginUsesExplicitUserPermissions(t *testing.T) {
	client := Client{Runner: func(_ context.Context, _ Client, _ io.Reader, args ...string) ([]byte, error) {
		if !slices.Equal(args, []string{"auth", "login", "--no-wait", "--json", "--scope", UserAuthorizationScopes()}) {
			t.Fatalf("unexpected login arguments: %v", args)
		}
		return []byte(`{"verification_url":"https://accounts.feishu.cn/verify","device_code":"device","expires_in":600,"requested_scope":"calendar:calendar:read"}`), nil
	}}
	device, err := client.Login(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !CurrentUserAuthorizationScopes(device.RequestedScope) {
		t.Fatal("requested permissions do not reflect the login request")
	}
}

func TestAutoApprovePermissionPolicy(t *testing.T) {
	for identity, scopes := range map[string][]string{"user": autoApproveScopes.User, "tenant": autoApproveScopes.Tenant} {
		if len(scopes) == 0 || !slices.IsSorted(scopes) || len(slices.Compact(slices.Clone(scopes))) != len(scopes) {
			t.Fatalf("%s permissions must be nonempty, sorted and unique", identity)
		}
		for _, scope := range []string{"calendar:calendar:read", "search:docs:read", "attendance:task:readonly", "mail:user_mailbox.message:send", "task:task:write", "task:tasklist:write", "vc:record:readonly", "vc:meeting.meetingevent:read"} {
			if slices.Contains(scopes, scope) {
				t.Errorf("%s policy requests approval-required permission %s", identity, scope)
			}
		}
	}
	for _, scope := range []string{"im:message.urgent", "mail:user_mailbox:readonly", "mail:user_mailbox.message:modify"} {
		if slices.Contains(autoApproveScopes.Tenant, scope) {
			t.Errorf("tenant policy requests reviewed permission or data access range: %s", scope)
		}
	}
	if !slices.Contains(autoApproveScopes.Tenant, "application:application:self_manage") || !slices.Contains(autoApproveScopes.Tenant, "im:message:send_as_bot") {
		t.Fatal("application inspection and bot messaging permissions are required")
	}
	if !slices.Contains(autoApproveScopes.User, "docx:document:create") || !slices.Contains(autoApproveScopes.User, "task:task:read") {
		t.Fatal("expected user permissions are unavailable")
	}
	reordered := slices.Clone(autoApproveScopes.User)
	slices.Reverse(reordered)
	if !CurrentUserAuthorizationScopes(strings.Join(reordered, " ")) {
		t.Fatal("scope order must not invalidate an authorization")
	}
	for _, old := range []string{"", "docx:document:create", UserAuthorizationScopes() + " calendar:calendar:read"} {
		if CurrentUserAuthorizationScopes(old) {
			t.Fatal("obsolete authorization must be replaced")
		}
	}
}

func TestInitRejectsInvalidAuthorizationURL(t *testing.T) {
	client := Client{
		URL: func(string) { t.Error("invalid URL reached the UI") },
		Runner: func(ctx context.Context, client Client, _ io.Reader, _ ...string) ([]byte, error) {
			client.URL("https://attacker.example/verify")
			if ctx.Err() == nil {
				t.Error("registration was not cancelled")
			}
			return nil, ctx.Err()
		},
	}
	if err := client.Init(context.Background()); err == nil {
		t.Fatal("invalid authorization URL accepted")
	}
}
