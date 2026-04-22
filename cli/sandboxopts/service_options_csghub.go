//go:build csghub
// +build csghub

package sandboxopts

import (
	"csgclaw/internal/agent"
	"csgclaw/internal/config"
	"csgclaw/internal/sandbox/csghub"
)

// ServiceOptions wires the CSGHub sandbox provider for csghub builds.
// Provider params are resolved from env inside sandbox/csghub so the
// agent layer stays backend-agnostic.
func ServiceOptions(_ config.SandboxConfig) ([]agent.ServiceOption, error) {
	provider, err := csghub.NewProviderFromEnv()
	if err != nil {
		return nil, err
	}
	return []agent.ServiceOption{
		agent.WithSandboxProvider(provider),
		agent.WithSandboxHomeDirName(config.DefaultSandboxHomeDirName),
	}, nil
}
