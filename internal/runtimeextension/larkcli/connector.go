package larkcli

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"strings"
	"time"

	"csgclaw/internal/identity"
	agentruntime "csgclaw/internal/runtime"
)

const ConnectorSourceProvider = "connector-feishu"

func IsConfigurationEnv(key string) bool {
	key = agentruntime.CanonicalEnvironmentKey(key)
	if strings.HasPrefix(key, "LARK_") || strings.HasPrefix(key, "LARKSUITE_") {
		return true
	}
	switch key {
	case "OPENCLAW_CLI", "OPENCLAW_HOME", "OPENCLAW_STATE_DIR", "OPENCLAW_CONFIG_PATH", "OPENCLAW_SERVICE_MARKER", "OPENCLAW_SERVICE_VERSION", "OPENCLAW_GATEWAY_PORT", "OPENCLAW_SHELL", "HERMES_HOME", "HERMES_QUIET", "HERMES_EXEC_ASK", "HERMES_GATEWAY_TOKEN", "HERMES_SESSION_KEY":
		return true
	}
	return false
}

func DisabledConfigDir(home string) string {
	return filepath.Join(home, "connectors", "feishu-disabled")
}

func (d *Driver) validateConnector(ctx context.Context, agentID string, p Payload) error {
	if identity.CanonicalAgentID(p.AgentID) != identity.CanonicalAgentID(agentID) || p.ResourceID == "" || !filepath.IsAbs(p.ConfigDir) || !filepath.IsAbs(p.Executable) || p.AppID == "" {
		return errors.New("invalid connector configuration")
	}
	appID, ok := readConfigAppID(filepath.Join(p.ConfigDir, ConfigFileName))
	if !ok || appID != p.AppID {
		return errors.New("connector application configuration does not match")
	}
	probe, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if err := d.options.CommandContext(probe, p.Executable, "-v").Run(); err != nil {
		return errors.New("connector executable is unavailable")
	}
	return nil
}

func connectorEnvironment(p Payload) map[string]string {
	return map[string]string{"LARKSUITE_CLI_CONFIG_DIR": p.ConfigDir}
}

func (d *Driver) prepareConnector(ctx context.Context, agentID string, desired agentruntime.ExtensionDesired, p Payload) (agentruntime.PreparedExtension, agentruntime.ExtensionResult, error) {
	now := time.Now().UTC()
	if err := d.validateConnector(ctx, agentID, p); err != nil {
		return nil, errorResult("connector_unavailable", err, now), err
	}
	_, store, err := d.store(agentID)
	if err != nil {
		return nil, agentruntime.ExtensionResult{}, err
	}
	current, found, err := store.Load(desired.Name)
	if err != nil {
		return nil, agentruntime.ExtensionResult{}, err
	}
	result := agentruntime.ExtensionResult{State: agentruntime.ExtensionStateConfigured, Reason: "configured", CheckedAt: now}
	if found && current.SourceRevision == desired.SourceRevision && current.Executable == p.Executable && maps.Equal(current.Environment, connectorEnvironment(p)) && current.Instructions == connectorInstructions(p) {
		current.Generation = desired.Generation
		change, err := store.Revise(current)
		return change, result, err
	}
	change, err := store.Stage(desired.Name)
	if err != nil {
		return nil, agentruntime.ExtensionResult{}, err
	}
	change.SetProjection(agentruntime.ExtensionProjection{Name: desired.Name, Kind: desired.Kind, Generation: desired.Generation, SourceRevision: desired.SourceRevision, Executable: p.Executable, Environment: connectorEnvironment(p), Instructions: connectorInstructions(p)})
	return change, result, nil
}

func (d *Driver) observeConnector(ctx context.Context, agentID string, desired agentruntime.ExtensionDesired, p Payload) (agentruntime.ExtensionResult, error) {
	now := time.Now().UTC()
	if err := d.validateConnector(ctx, agentID, p); err != nil {
		return errorResult("connector_unavailable", err, now), nil
	}
	_, store, err := d.store(agentID)
	if err != nil {
		return agentruntime.ExtensionResult{}, err
	}
	projection, found, err := store.Load(desired.Name)
	if err != nil || !found || projection.SourceRevision != desired.SourceRevision || projection.Generation != desired.Generation || projection.Executable != p.Executable || !maps.Equal(projection.Environment, connectorEnvironment(p)) || projection.Instructions != connectorInstructions(p) {
		return errorResult("binding_mismatch", errors.New("connector projection does not match"), now), nil
	}
	return agentruntime.ExtensionResult{State: agentruntime.ExtensionStateConfigured, Reason: "configured", CheckedAt: now, RuntimeLoaded: d.options.RuntimeLoaded != nil && d.options.RuntimeLoaded(agentID, projection)}, nil
}

func connectorInstructions(p Payload) string {
	if !p.HistoricalAttachments {
		return ""
	}
	index := strings.Index(managedInstructionsTemplate, "### Feishu Historical Attachment Recovery")
	return managedInstructionsTemplate[index:]
}
