package sandboxproviders

import (
	"csgclaw/internal/agent"
	"csgclaw/internal/config"
	"csgclaw/internal/sandbox/csghub"
)

func init() {
	Register(config.CSGHubProvider, func(cfg config.SandboxConfig) (agent.ServiceOption, error) {
		return agent.WithSandboxProvider(csghub.NewProvider(csghub.WithPVCMountSubpathPrefix(cfg.StoragePath))), nil
	})
}
