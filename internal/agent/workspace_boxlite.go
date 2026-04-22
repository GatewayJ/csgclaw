//go:build !csghub
// +build !csghub

package agent

import "path/filepath"

func ensureAgentWorkspace(agentName, template string) (string, error) {
	hostRoot, err := agentWorkspaceRoot(agentName)
	if err != nil {
		return "", err
	}
	return ensureAgentWorkspaceAt(hostRoot, template)
}

func agentWorkspaceRoot(agentName string) (string, error) {
	agentHome, err := agentHomeDir(agentName)
	if err != nil {
		return "", err
	}
	return filepath.Join(agentHome, hostWorkspaceDir), nil
}
