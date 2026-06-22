package participant

import (
	"fmt"
	"strings"
)

const (
	ChannelAppConfigAppSecretKey         = "app_secret"
	ChannelAppConfigVerificationTokenKey = "verification_token"
	ChannelAppConfigEncryptKeyKey        = "encrypt_key"
	RedactedSecretValue                  = "present"
)

func RedactChannelAppConfig(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		if isSecretChannelAppConfigKey(key) &&
			strings.TrimSpace(fmt.Sprint(value)) != "" {
			out[key] = RedactedSecretValue
			continue
		}
		out[key] = value
	}
	return out
}

func isSecretChannelAppConfigKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case ChannelAppConfigAppSecretKey, ChannelAppConfigVerificationTokenKey, ChannelAppConfigEncryptKeyKey:
		return true
	default:
		return false
	}
}
