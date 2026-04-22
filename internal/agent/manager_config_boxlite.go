//go:build !csghub
// +build !csghub

package agent

import (
	"path/filepath"

	"csgclaw/internal/config"
)

func ensureAgentPicoClawConfig(agentName, botID string, server config.ServerConfig, model config.ModelConfig) (string, error) {
	hostRoot, err := agentPicoClawRoot(agentName)
	if err != nil {
		return "", err
	}
	return ensureAgentPicoClawConfigAt(hostRoot, botID, server, model)
}

func agentPicoClawRoot(agentName string) (string, error) {
	home, err := agentHomeDir(agentName)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, hostPicoClawDir), nil
}
