//go:build csghub
// +build csghub

package csghub

import (
	"fmt"

	"csgclaw/internal/config"
)

// NewProviderFromEnv builds a provider from the canonical csghub env
// contract, keeping env parsing inside sandbox/csghub.
func NewProviderFromEnv() (Provider, error) {
	env, err := config.LoadCSGHubSandboxEnv()
	if err != nil {
		return Provider{}, fmt.Errorf("load csghub sandbox env: %w", err)
	}
	defaults, err := LoadAgentDefaultsFromEnv()
	if err != nil {
		return Provider{}, fmt.Errorf("load csghub agent defaults: %w", err)
	}
	p := NewProvider(Params{
		BaseURL:      env.HubAPIBase,
		AIGatewayURL: env.HubAIGatewayURL,
		Token:        env.HubUserToken,
		ClusterID:    env.ClusterID,
		ResourceID:   env.ResourceID,
		Port:         env.Port,
		Timeout:      env.Timeout,
		ReadyTimeout: env.ReadyTimeout,
		PollInterval: env.PollInterval,
	})
	p.agent = defaults
	return p, nil
}
