package feishubind

import (
	"testing"

	"csgclaw/internal/participant"
)

func TestFeishuBotAppConfigStoresOnlyLongConnectionCredentials(t *testing.T) {
	got := feishuBotAppConfig(" cli_new ", " new-secret ")
	if got["app_id"] != "cli_new" {
		t.Fatalf("app_id = %#v, want %q", got["app_id"], "cli_new")
	}
	if got[participant.ChannelAppConfigAppSecretKey] != "new-secret" {
		t.Fatalf("app_secret = %#v, want %q", got[participant.ChannelAppConfigAppSecretKey], "new-secret")
	}
	if len(got) != 2 {
		t.Fatalf("config = %#v, want only app_id and app_secret", got)
	}
}
