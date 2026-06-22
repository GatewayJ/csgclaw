package participant

import "testing"

func TestRedactChannelAppConfigMasksSecretWithoutMutatingInput(t *testing.T) {
	values := map[string]any{
		"app_id":                             "cli_dev",
		ChannelAppConfigAppSecretKey:         "dev-secret",
		ChannelAppConfigVerificationTokenKey: "verify-token",
		ChannelAppConfigEncryptKeyKey:        "encrypt-key",
	}

	got := RedactChannelAppConfig(values)

	if got["app_id"] != "cli_dev" {
		t.Fatalf("app_id = %#v, want cli_dev", got["app_id"])
	}
	if got[ChannelAppConfigAppSecretKey] != RedactedSecretValue {
		t.Fatalf("app_secret = %#v, want %q", got[ChannelAppConfigAppSecretKey], RedactedSecretValue)
	}
	if got[ChannelAppConfigVerificationTokenKey] != RedactedSecretValue {
		t.Fatalf("verification_token = %#v, want %q", got[ChannelAppConfigVerificationTokenKey], RedactedSecretValue)
	}
	if got[ChannelAppConfigEncryptKeyKey] != RedactedSecretValue {
		t.Fatalf("encrypt_key = %#v, want %q", got[ChannelAppConfigEncryptKeyKey], RedactedSecretValue)
	}
	if values[ChannelAppConfigAppSecretKey] != "dev-secret" {
		t.Fatalf("input app_secret = %#v, want original secret preserved", values[ChannelAppConfigAppSecretKey])
	}
}
