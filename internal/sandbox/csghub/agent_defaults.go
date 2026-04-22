//go:build csghub
// +build csghub

package csghub

import (
	"strings"

	"csgclaw/internal/config"
)

func LoadAgentDefaultsFromEnv() (AgentDefaults, error) {
	env, err := config.LoadCSGHubSandboxEnv()
	if err != nil {
		return AgentDefaults{}, err
	}
	params := fillMountPathEnv(Params{})
	scope := sanitizeNameScope(params.subpathRoot)

	downstream := map[string]string{
		config.EnvHubAPIBase:   env.HubAPIBase,
		config.EnvHubUserToken: env.HubUserToken,
		config.EnvHubAIGateway: env.HubAIGatewayURL,
	}
	if strings.TrimSpace(env.HubUserName) != "" {
		downstream[config.EnvHubUserName] = strings.TrimSpace(env.HubUserName)
	}
	return AgentDefaults{
		ManagerImage: strings.TrimSpace(env.Image),
		NameScope:    scope,
		Downstream:   downstream,
	}, nil
}

func sanitizeNameScope(raw string) string {
	scope := strings.NewReplacer("/", "-", "_", "-", ".", "-").Replace(strings.TrimSpace(raw))
	return strings.Trim(scope, "-")
}
