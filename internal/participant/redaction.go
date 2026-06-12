package participant

import (
	"fmt"
	"strings"
)

const (
	ChannelAppConfigAppSecretKey = "app_secret"
	RedactedSecretValue          = "present"
)

func RedactChannelAppConfig(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		if strings.EqualFold(strings.TrimSpace(key), ChannelAppConfigAppSecretKey) &&
			strings.TrimSpace(fmt.Sprint(value)) != "" {
			out[key] = RedactedSecretValue
			continue
		}
		out[key] = value
	}
	return out
}
