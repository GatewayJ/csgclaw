package participant

import "testing"

func TestRedactChannelAppConfigMasksSecretWithoutMutatingInput(t *testing.T) {
	values := map[string]any{
		"app_id":                     "cli_dev",
		ChannelAppConfigAppSecretKey: "dev-secret",
	}

	got := RedactChannelAppConfig(values)

	if got["app_id"] != "cli_dev" {
		t.Fatalf("app_id = %#v, want cli_dev", got["app_id"])
	}
	if got[ChannelAppConfigAppSecretKey] != RedactedSecretValue {
		t.Fatalf("app_secret = %#v, want %q", got[ChannelAppConfigAppSecretKey], RedactedSecretValue)
	}
	if values[ChannelAppConfigAppSecretKey] != "dev-secret" {
		t.Fatalf("input app_secret = %#v, want original secret preserved", values[ChannelAppConfigAppSecretKey])
	}
}
