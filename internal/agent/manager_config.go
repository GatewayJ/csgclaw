package agent

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"csgclaw/internal/config"
)

// managerAgentsDirName is the sub-directory that holds per-agent state under
// the server's home root (shared between both deployment backends).

const managerAgentsDirName = "agents"

//go:embed defaults/picoclaw-config.json
var defaultManagerPicoClawConfig []byte

//go:embed defaults/manager-security.yml
var defaultManagerSecurityConfig string

func ensureAgentPicoClawConfigAt(hostRoot, botID string, server config.ServerConfig, model config.ModelConfig) (string, error) {
	if err := os.MkdirAll(filepath.Join(hostRoot, hostPicoClawLogs), 0o755); err != nil {
		return "", fmt.Errorf("create manager picoclaw logs dir: %w", err)
	}

	data, err := renderAgentPicoClawConfig(botID, server, model)
	if err != nil {
		return "", err
	}
	configPath := filepath.Join(hostRoot, hostPicoClawConfig)
	if err := os.WriteFile(configPath, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write manager picoclaw config: %w", err)
	}
	securityData := renderManagerSecurityConfig(server, model)
	securityPath := filepath.Join(hostRoot, ".security.yml")
	if err := os.WriteFile(securityPath, []byte(securityData), 0o600); err != nil {
		return "", fmt.Errorf("write manager security config: %w", err)
	}
	return hostRoot, nil
}

func renderAgentPicoClawConfig(botID string, server config.ServerConfig, model config.ModelConfig) ([]byte, error) {
	var cfg map[string]any
	if err := json.Unmarshal(defaultManagerPicoClawConfig, &cfg); err != nil {
		return nil, fmt.Errorf("decode embedded manager picoclaw config: %w", err)
	}

	if err := updateModelList(cfg, botID, server, model); err != nil {
		return nil, err
	}
	if err := updateCSGClawChannel(cfg, botID, server); err != nil {
		return nil, err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode manager picoclaw config: %w", err)
	}
	return data, nil
}

func updateModelList(cfg map[string]any, botID string, server config.ServerConfig, modelCfg config.ModelConfig) error {
	modelList, ok := cfg["model_list"].([]any)
	if !ok || len(modelList) == 0 {
		return fmt.Errorf("embedded manager picoclaw config is missing model_list[0]")
	}
	model, ok := modelList[0].(map[string]any)
	if !ok {
		return fmt.Errorf("embedded manager picoclaw config has invalid model_list[0]")
	}
	if modelID := strings.TrimSpace(modelCfg.ModelID); modelID != "" {
		model["model_name"] = modelID
		model["model"] = picoclawBridgeModelID(modelID)
	}
	if agents, ok := cfg["agents"].(map[string]any); ok {
		if defaults, ok := agents["defaults"].(map[string]any); ok {
			if modelID := strings.TrimSpace(modelCfg.ModelID); modelID != "" {
				defaults["model_name"] = modelID
			}
		}
	}

	if managerBaseURL := resolveManagerBaseURL(server); managerBaseURL != "" {
		model["api_base"] = llmBridgeBaseURL(managerBaseURL, botID)
	}
	if server.AccessToken != "" {
		model["api_key"] = server.AccessToken
	}
	return nil
}

func picoclawBridgeModelID(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(modelID), "openai/") {
		return modelID
	}
	if prefix, rest, ok := strings.Cut(modelID, ":"); ok && strings.EqualFold(strings.TrimSpace(prefix), "openai") && strings.TrimSpace(rest) != "" {
		return "openai/" + strings.TrimSpace(rest)
	}
	// The local bridge always exposes an OpenAI-compatible endpoint, so the
	// PicoClaw model entry must use the OpenAI protocol even if the upstream
	// model identifier itself contains slashes.
	return "openai/" + modelID
}

func updateCSGClawChannel(cfg map[string]any, botID string, server config.ServerConfig) error {
	channels, ok := cfg["channels"].(map[string]any)
	if !ok {
		return fmt.Errorf("embedded manager picoclaw config is missing channels")
	}
	channel, ok := channels["csgclaw"].(map[string]any)
	if !ok {
		return fmt.Errorf("embedded manager picoclaw config is missing channels.csgclaw")
	}
	if baseURL := resolveManagerBaseURL(server); baseURL != "" {
		channel["base_url"] = baseURL
	}
	if server.AccessToken != "" {
		channel["access_token"] = server.AccessToken
	}
	channel["bot_id"] = botID
	channel["enabled"] = true
	return nil
}

func renderManagerSecurityConfig(server config.ServerConfig, model config.ModelConfig) string {
	modelID := model.ModelID
	apiKey := strings.TrimSpace(server.AccessToken)
	if apiKey == "" {
		apiKey = model.APIKey
	}

	content := strings.ReplaceAll(defaultManagerSecurityConfig, "__MODEL_ID__", modelID)
	content = strings.ReplaceAll(content, "__API_KEY__", apiKey)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content
}
