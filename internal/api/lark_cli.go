package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"csgclaw/internal/agent"
	"csgclaw/internal/apitypes"
	"csgclaw/internal/config"
	"csgclaw/internal/participant"
	agentruntime "csgclaw/internal/runtime"
)

const (
	larkCLIConfigDirName          = "lark-cli"
	larkCLISourceDirName          = "lark-cli-source"
	larkCLIBindMarkerFileName     = "bound.json"
	larkCLIWorkspaceName          = "lark-channel"
	larkCLIConfigFileName         = "config.json"
	larkCLISourceConfigFileName   = "config.json"
	larkCLISourceTokenPrefix      = "larkcli-src-v1"
	larkCLISourceTokenPurpose     = "feishu_app_info"
	larkCLISourceProviderName     = "csgclaw-pt"
	larkCLIAppSecretExecID        = "app_secret"
	larkCLIIdentityPreset         = "bot-only"
	larkCLIDefaultNPMPackage      = "@larksuite/cli@latest"
	feishuBotNotConfiguredCode    = "feishu_bot_not_configured"
	feishuBotAppIDConflictCode    = "feishu_bot_app_id_conflict"
	larkCLIInstallTimeout         = 3 * time.Minute
	larkCLIBindTimeout            = 90 * time.Second
	larkCLIExecProviderTimeoutMS  = 10_000
	larkCLIExecProviderMaxBytes   = 64 * 1024
	larkCLICommandOutputMaxDetail = 2000
)

var (
	errFeishuBotNotConfigured = errors.New("feishu bot is not configured")
	errFeishuBotAppIDConflict = errors.New("feishu bot app_id is already used by another worker")

	larkCLILookPath       = exec.LookPath
	larkCLICommandContext = exec.CommandContext
	larkCLICurrentExe     = os.Executable
)

type feishuBotAppInfo struct {
	AgentID       string
	ParticipantID string
	AppID         string
	AppSecret     string
}

type larkCLISourceTokenPayload struct {
	Version string `json:"version"`
	Purpose string `json:"purpose"`
	AgentID string `json:"agent_id"`
}

func (h *Handler) initAgentLarkCLI(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		http.Error(w, "agent service is not configured", http.StatusServiceUnavailable)
		return
	}
	if h.participant == nil {
		http.Error(w, "participant service is not configured", http.StatusServiceUnavailable)
		return
	}
	agentID := strings.TrimSpace(pathValue(r, "id"))
	if agentID == "" {
		http.NotFound(w, r)
		return
	}
	target, ok := h.svc.Agent(agentID)
	if !ok {
		writeAgentOperationError(w, fmt.Errorf("agent %q not found", agent.CanonicalID(agentID)), http.StatusNotFound)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(target.RuntimeKind), agent.RuntimeKindCodex) {
		writeCodedAPIError(w, http.StatusBadRequest, "unsupported_runtime", "lark-cli init is supported only for Codex workers")
		return
	}

	appInfo, err := h.feishuBotAppInfoForAgent(target.ID)
	if err != nil {
		h.writeFeishuBotAppInfoError(w, err)
		return
	}
	if err := h.validateFeishuBotAppIDExclusive(target.ID, appInfo.AppID); err != nil {
		h.writeFeishuBotAppInfoError(w, err)
		return
	}
	accessToken, err := h.sourceAccessToken(target.ID)
	if err != nil {
		writeCodedAPIError(w, http.StatusServiceUnavailable, "lark_cli_source_auth_unavailable", err.Error())
		return
	}

	layout, err := h.svc.AgentLayout(target.ID)
	if err != nil {
		writeAgentOperationError(w, err, http.StatusBadRequest)
		return
	}
	codexHomeDir := codexHomeDirFromLayout(layout)
	if codexHomeDir == "" {
		writeCodedAPIError(w, http.StatusBadRequest, "codex_home_unavailable", "agent Codex home directory is unavailable")
		return
	}
	configDir := filepath.Join(codexHomeDir, larkCLIConfigDirName)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		http.Error(w, fmt.Sprintf("create lark-cli config dir: %v", err), http.StatusInternalServerError)
		return
	}
	_ = os.Chmod(configDir, 0o700)

	larkCLIPath, installed, err := ensureLarkCLI(r.Context())
	if err != nil {
		writeCodedAPIError(w, http.StatusServiceUnavailable, "lark_cli_unavailable", err.Error())
		return
	}
	helperPath, err := larkCLISourceHelperPath()
	if err != nil {
		writeCodedAPIError(w, http.StatusServiceUnavailable, "lark_cli_source_unavailable", err.Error())
		return
	}

	sourceDir := filepath.Join(codexHomeDir, larkCLISourceDirName)
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		http.Error(w, fmt.Sprintf("create lark-cli source dir: %v", err), http.StatusInternalServerError)
		return
	}
	_ = os.Chmod(sourceDir, 0o700)

	sourcePath := filepath.Join(sourceDir, larkCLISourceConfigFileName)
	bindMarkerPath := filepath.Join(sourceDir, larkCLIBindMarkerFileName)
	previousSource, hadPreviousSource, err := readOptionalFile(sourcePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("read previous lark-cli source config: %v", err), http.StatusInternalServerError)
		return
	}
	previousMarker, hadPreviousMarker, err := readOptionalFile(bindMarkerPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("read previous lark-cli bind marker: %v", err), http.StatusInternalServerError)
		return
	}
	if err := writeLarkChannelSourceConfig(sourcePath, larkChannelSourceConfig{
		AppID:       appInfo.AppID,
		BaseURL:     h.internalSourceBaseURL(r),
		AccessToken: accessToken,
		HelperPath:  helperPath,
		AgentID:     target.ID,
	}); err != nil {
		http.Error(w, fmt.Sprintf("write lark-cli source config: %v", err), http.StatusInternalServerError)
		return
	}

	if err := runLarkCLIConfigBind(r.Context(), larkCLIPath, configDir, sourcePath, codexHomeDir, target.ID); err != nil {
		if restoreErr := restoreLarkChannelBinding(sourcePath, bindMarkerPath, previousSource, hadPreviousSource, previousMarker, hadPreviousMarker); restoreErr != nil {
			slog.Warn("restore lark-cli source config after bind failure failed", "agent_id", target.ID, "error", restoreErr)
		}
		writeCodedAPIError(w, http.StatusBadGateway, "lark_cli_bind_failed", err.Error())
		return
	}
	if err := writeLarkCLIBindMarker(bindMarkerPath, larkCLIBindMarker{
		AgentID:          target.ID,
		AppID:            appInfo.AppID,
		ConfigPath:       filepath.Join(configDir, larkCLIWorkspaceName, larkCLIConfigFileName),
		SourceConfigPath: sourcePath,
		BoundAt:          time.Now().UTC(),
	}); err != nil {
		http.Error(w, fmt.Sprintf("write lark-cli bind marker: %v", err), http.StatusInternalServerError)
		return
	}
	if err := h.refreshAgentInstructionsForLarkCLI(r.Context(), target); err != nil {
		slog.Warn("refresh lark-cli managed instructions failed", "agent_id", target.ID, "error", err)
	}
	restartStatus := "restart_skipped"
	var restartError string
	if restarted, err := h.restartAgentCodexRuntimeForLarkCLI(r.Context(), target.ID); err != nil {
		restartStatus = "restart_failed"
		restartError = err.Error()
		slog.Warn("restart codex worker after lark-cli init failed", "agent_id", target.ID, "error", err)
	} else if restarted {
		restartStatus = "runtime_restarted"
	}

	writeJSON(w, http.StatusOK, apitypes.AgentLarkCLIInitResponse{
		Status:           "configured",
		AgentID:          target.ID,
		ParticipantID:    appInfo.ParticipantID,
		AppID:            appInfo.AppID,
		Installed:        installed,
		LarkCLIPath:      larkCLIPath,
		ConfigDir:        configDir,
		ConfigPath:       filepath.Join(configDir, larkCLIWorkspaceName, larkCLIConfigFileName),
		SourceConfigPath: sourcePath,
		RestartStatus:    restartStatus,
		RestartError:     restartError,
	})
}

func (h *Handler) refreshAgentInstructionsForLarkCLI(ctx context.Context, target agent.Agent) error {
	if h == nil || h.svc == nil {
		return nil
	}
	instructions := target.Instructions
	_, err := h.svc.Update(ctx, target.ID, agent.UpdateRequest{
		Instructions: &instructions,
		FieldMask:    []string{"instructions"},
	})
	return err
}

func (h *Handler) restartAgentCodexRuntimeForLarkCLI(ctx context.Context, agentID string) (bool, error) {
	if h == nil || h.svc == nil {
		return false, nil
	}
	_, restarted, err := h.svc.RestartCodexRuntimeIfRunning(ctx, agentID)
	return restarted, err
}

func (h *Handler) clearAgentLarkCLIState(ctx context.Context, agentID string) error {
	if h == nil || h.svc == nil {
		return nil
	}
	target, ok := h.svc.Agent(agentID)
	if !ok {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(target.RuntimeKind), agent.RuntimeKindCodex) {
		return nil
	}
	layout, err := h.svc.AgentLayout(target.ID)
	if err != nil {
		return err
	}
	codexHomeDir := codexHomeDirFromLayout(layout)
	if codexHomeDir == "" {
		return fmt.Errorf("agent %q Codex home directory is unavailable", target.ID)
	}

	var errs []error
	for _, dir := range []string{
		filepath.Join(codexHomeDir, larkCLIConfigDirName),
		filepath.Join(codexHomeDir, larkCLISourceDirName),
	} {
		if err := os.RemoveAll(dir); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", dir, err))
		}
	}
	if err := h.refreshAgentInstructionsForLarkCLI(ctx, target); err != nil {
		errs = append(errs, fmt.Errorf("refresh lark-cli managed instructions: %w", err))
	}
	if _, err := h.restartAgentCodexRuntimeForLarkCLI(ctx, target.ID); err != nil {
		errs = append(errs, fmt.Errorf("restart codex worker after clearing lark-cli state: %w", err))
	}
	return errors.Join(errs...)
}

func codexHomeDirFromLayout(layout agentruntime.Layout) string {
	if instructionsPath := strings.TrimSpace(layout.InstructionsPath); instructionsPath != "" {
		dir := strings.TrimSpace(filepath.Dir(instructionsPath))
		if dir != "." {
			return dir
		}
	}
	if skillsRoot := strings.TrimSpace(layout.SkillsRoot); skillsRoot != "" && filepath.Base(skillsRoot) == "skills" {
		dir := strings.TrimSpace(filepath.Dir(skillsRoot))
		if dir != "." {
			return dir
		}
	}
	return ""
}

func (h *Handler) getAgentFeishuAppInfo(w http.ResponseWriter, r *http.Request) {
	agentID := pathValue(r, "id")
	if !h.validateLarkCLISourceAccessToken(r.Header.Get("Authorization"), agentID) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	appInfo, err := h.feishuBotAppInfoForAgent(agentID)
	if err != nil {
		h.writeFeishuBotAppInfoError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apitypes.FeishuBotAppInfo{
		AgentID:       appInfo.AgentID,
		ParticipantID: appInfo.ParticipantID,
		AppID:         appInfo.AppID,
		AppSecret:     appInfo.AppSecret,
	})
}

func (h *Handler) feishuBotAppInfoForAgent(agentID string) (feishuBotAppInfo, error) {
	if h.svc == nil {
		return feishuBotAppInfo{}, fmt.Errorf("agent service is required")
	}
	if h.participant == nil {
		return feishuBotAppInfo{}, fmt.Errorf("participant service is required")
	}
	agentID = strings.TrimSpace(agentID)
	target, ok := h.svc.Agent(agentID)
	if !ok {
		return feishuBotAppInfo{}, fmt.Errorf("agent %q not found", agent.CanonicalID(agentID))
	}

	canonicalParticipantID := agent.ParticipantIDForAgent(target.Name, target.ID)
	if item, ok := h.participant.Get(participant.ChannelFeishu, canonicalParticipantID); ok {
		if info, ok := feishuBotAppInfoFromParticipant(target.ID, item); ok {
			return info, nil
		}
	}
	for _, item := range h.participant.List(participant.ListOptions{
		Channel: participant.ChannelFeishu,
		Type:    participant.TypeAgent,
		AgentID: target.ID,
	}) {
		if info, ok := feishuBotAppInfoFromParticipant(target.ID, item); ok {
			return info, nil
		}
	}
	return feishuBotAppInfo{}, fmt.Errorf("%w for agent %q", errFeishuBotNotConfigured, target.ID)
}

func feishuBotAppInfoFromParticipant(agentID string, item apitypes.Participant) (feishuBotAppInfo, bool) {
	if !strings.EqualFold(strings.TrimSpace(item.Channel), participant.ChannelFeishu) ||
		strings.TrimSpace(item.Type) != participant.TypeAgent ||
		strings.TrimSpace(item.AgentID) != strings.TrimSpace(agentID) {
		return feishuBotAppInfo{}, false
	}
	appID := channelAppConfigString(item.ChannelAppConfig, "app_id")
	appSecret := channelAppConfigString(item.ChannelAppConfig, participant.ChannelAppConfigAppSecretKey)
	if appID == "" || appSecret == "" || appSecret == participant.RedactedSecretValue {
		return feishuBotAppInfo{}, false
	}
	return feishuBotAppInfo{
		AgentID:       strings.TrimSpace(agentID),
		ParticipantID: strings.TrimSpace(item.ID),
		AppID:         appID,
		AppSecret:     appSecret,
	}, true
}

func (h *Handler) validateFeishuBotAppIDExclusive(agentID, appID string) error {
	if h == nil || h.participant == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return nil
	}
	for _, item := range h.participant.List(participant.ListOptions{
		Channel: participant.ChannelFeishu,
		Type:    participant.TypeAgent,
	}) {
		if strings.TrimSpace(item.AgentID) == agentID {
			continue
		}
		if strings.TrimSpace(item.AgentID) == "" {
			continue
		}
		if channelAppConfigString(item.ChannelAppConfig, "app_id") == appID {
			return fmt.Errorf("%w: app_id %q is used by agent %q", errFeishuBotAppIDConflict, appID, item.AgentID)
		}
	}
	return nil
}

func (h *Handler) writeFeishuBotAppInfoError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errFeishuBotNotConfigured):
		writeCodedAPIError(w, http.StatusConflict, feishuBotNotConfiguredCode, "This worker has not configured a Feishu bot app yet.")
	case errors.Is(err, errFeishuBotAppIDConflict):
		writeCodedAPIError(w, http.StatusConflict, feishuBotAppIDConflictCode, err.Error())
	default:
		writeAgentOperationError(w, err, http.StatusBadRequest)
	}
}

func writeCodedAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    strings.TrimSpace(code),
			"message": strings.TrimSpace(message),
		},
	})
}

func channelAppConfigString(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	for k, value := range values {
		if strings.EqualFold(strings.TrimSpace(k), key) {
			return strings.TrimSpace(fmt.Sprint(value))
		}
	}
	return ""
}

func ensureLarkCLI(ctx context.Context) (string, bool, error) {
	if path, err := larkCLILookPath("lark-cli"); err == nil && strings.TrimSpace(path) != "" {
		return path, false, nil
	}
	npmPath, err := larkCLILookPath("npm")
	if err != nil {
		return "", false, fmt.Errorf("lark-cli is not installed and npm is not available to install %s: %w", larkCLINPMPackage(), err)
	}

	installCtx, cancel := context.WithTimeout(ctx, larkCLIInstallTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := larkCLICommandContext(installCtx, npmPath, "install", "-g", larkCLINPMPackage())
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return "", true, fmt.Errorf("install lark-cli with npm install -g failed: %w%s", err, commandOutputDetail(stdout.String(), stderr.String()))
	}
	if path, err := larkCLILookPath("lark-cli"); err == nil && strings.TrimSpace(path) != "" {
		return path, true, nil
	}
	if path, err := npmGlobalLarkCLIPath(ctx, npmPath); err == nil && strings.TrimSpace(path) != "" {
		return path, true, nil
	}
	return "", true, fmt.Errorf("installed %s, but lark-cli was not found on PATH", larkCLINPMPackage())
}

func larkCLINPMPackage() string {
	if value := strings.TrimSpace(os.Getenv("CSGCLAW_LARK_CLI_NPM_PACKAGE")); value != "" {
		return value
	}
	return larkCLIDefaultNPMPackage
}

func npmGlobalLarkCLIPath(ctx context.Context, npmPath string) (string, error) {
	cmd := larkCLICommandContext(ctx, npmPath, "prefix", "-g")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve npm global prefix: %w%s", err, commandOutputDetail("", stderr.String()))
	}
	prefix := strings.TrimSpace(string(out))
	if prefix == "" {
		return "", fmt.Errorf("npm global prefix is empty")
	}
	name := "lark-cli"
	if goruntime.GOOS == "windows" {
		name = "lark-cli.cmd"
	}
	candidates := []string{filepath.Join(prefix, "bin", name), filepath.Join(prefix, name)}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("lark-cli not found under npm global prefix %q", prefix)
}

func runLarkCLIConfigBind(ctx context.Context, larkCLIPath, configDir, sourcePath, channelHome, channelProfile string) error {
	bindCtx, cancel := context.WithTimeout(ctx, larkCLIBindTimeout)
	defer cancel()

	args := []string{
		"config", "bind",
		"--source", larkCLIWorkspaceName,
		"--identity", larkCLIIdentityPreset,
		"--force",
		"--lang", "zh",
	}
	var stdout, stderr bytes.Buffer
	cmd := larkCLICommandContext(bindCtx, larkCLIPath, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = mergedCommandEnv(os.Environ(), map[string]string{
		"LARKSUITE_CLI_CONFIG_DIR": configDir,
		"LARK_CHANNEL":             "1",
		"LARK_CHANNEL_HOME":        strings.TrimSpace(channelHome),
		"LARK_CHANNEL_PROFILE":     strings.TrimSpace(channelProfile),
		"LARK_CHANNEL_CONFIG":      sourcePath,
	})
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run lark-cli config bind: %w%s", err, commandOutputDetail(stdout.String(), stderr.String()))
	}
	return nil
}

type larkChannelSourceConfig struct {
	AppID       string
	BaseURL     string
	AccessToken string
	HelperPath  string
	AgentID     string
}

type larkCLIBindMarker struct {
	AgentID          string    `json:"agent_id"`
	AppID            string    `json:"app_id"`
	ConfigPath       string    `json:"config_path"`
	SourceConfigPath string    `json:"source_config_path"`
	BoundAt          time.Time `json:"bound_at"`
}

func writeLarkChannelSourceConfig(path string, cfg larkChannelSourceConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	payload := map[string]any{
		"accounts": map[string]any{
			"app": map[string]any{
				"id": cfg.AppID,
				"secret": map[string]any{
					"source":   "exec",
					"provider": larkCLISourceProviderName,
					"id":       larkCLIAppSecretExecID,
				},
				"tenant": "feishu",
			},
		},
		"secrets": map[string]any{
			"providers": map[string]any{
				larkCLISourceProviderName: map[string]any{
					"source":              "exec",
					"command":             cfg.HelperPath,
					"args":                []string{"pt", "app-info", "--channel", "feishu", "--agent-id", cfg.AgentID, "--exec-provider"},
					"env":                 sourceProviderEnv(cfg.BaseURL, cfg.AccessToken),
					"trustedDirs":         []string{filepath.Dir(cfg.HelperPath)},
					"allowInsecurePath":   true,
					"allowSymlinkCommand": true,
					"noOutputTimeoutMs":   larkCLIExecProviderTimeoutMS,
					"maxOutputBytes":      larkCLIExecProviderMaxBytes,
				},
			},
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return writeFile0600Atomic(path, append(data, '\n'))
}

func writeLarkCLIBindMarker(path string, marker larkCLIBindMarker) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return writeFile0600Atomic(path, append(data, '\n'))
}

func readOptionalFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return nil, false, err
}

func restoreLarkChannelBinding(sourcePath, bindMarkerPath string, previousSource []byte, hadPreviousSource bool, previousMarker []byte, hadPreviousMarker bool) error {
	if hadPreviousSource && hadPreviousMarker {
		if err := writeFile0600Atomic(sourcePath, previousSource); err != nil {
			return err
		}
		return writeFile0600Atomic(bindMarkerPath, previousMarker)
	}
	var errs []error
	if err := os.Remove(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	if err := os.Remove(bindMarkerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func writeFile0600Atomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func sourceProviderEnv(baseURL, token string) map[string]string {
	env := map[string]string{
		"CSGCLAW_BASE_URL": strings.TrimSpace(baseURL),
	}
	if strings.TrimSpace(token) != "" {
		env["CSGCLAW_ACCESS_TOKEN"] = strings.TrimSpace(token)
	}
	return env
}

func larkCLISourceHelperPath() (string, error) {
	path, err := larkCLICurrentExe()
	if err != nil {
		return "", fmt.Errorf("resolve csgclaw executable for lark-cli source: %w", err)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("csgclaw executable path is empty")
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("resolve csgclaw executable absolute path: %w", err)
		}
		path = abs
	}
	return path, nil
}

func (h *Handler) sourceAccessToken(agentID string) (string, error) {
	if h.serverNoAuth {
		return "", nil
	}
	return h.larkCLISourceAccessToken(agentID)
}

func (h *Handler) larkCLISourceAccessToken(agentID string) (string, error) {
	secrets := h.larkCLISourceSigningSecrets()
	if len(secrets) == 0 {
		return "", fmt.Errorf("CSGClaw API token is required for the lark-cli source command")
	}
	payload := larkCLISourceTokenPayload{
		Version: larkCLISourceTokenPrefix,
		Purpose: larkCLISourceTokenPurpose,
		AgentID: agent.CanonicalID(agentID),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(data)
	signature := signLarkCLISourceToken(encodedPayload, secrets[0])
	return larkCLISourceTokenPrefix + "." + encodedPayload + "." + signature, nil
}

func (h *Handler) validateLarkCLISourceAccessToken(authHeader, agentID string) bool {
	if h.serverNoAuth {
		return true
	}
	authHeader = strings.TrimSpace(authHeader)
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != larkCLISourceTokenPrefix {
		return false
	}
	payloadData, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var payload larkCLISourceTokenPayload
	if err := json.Unmarshal(payloadData, &payload); err != nil {
		return false
	}
	if payload.Version != larkCLISourceTokenPrefix ||
		payload.Purpose != larkCLISourceTokenPurpose ||
		payload.AgentID != agent.CanonicalID(agentID) {
		return false
	}
	for _, secret := range h.larkCLISourceSigningSecrets() {
		if hmac.Equal([]byte(parts[2]), []byte(signLarkCLISourceToken(parts[1], secret))) {
			return true
		}
	}
	return false
}

func (h *Handler) larkCLISourceSigningSecrets() []string {
	var secrets []string
	if h == nil {
		return nil
	}
	if token := strings.TrimSpace(h.serverAccessToken); token != "" {
		secrets = append(secrets, token)
	}
	if token := strings.TrimSpace(h.desktopSessionToken); token != "" {
		secrets = append(secrets, token)
	}
	return secrets
}

func signLarkCLISourceToken(encodedPayload, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encodedPayload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (h *Handler) internalSourceBaseURL(r *http.Request) string {
	if h != nil && strings.TrimSpace(h.advertiseBaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(h.advertiseBaseURL), "/")
	}
	if r != nil && strings.TrimSpace(r.Host) != "" {
		scheme := "http"
		if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto == "http" || proto == "https" {
			scheme = proto
		} else if r.TLS != nil {
			scheme = "https"
		}
		return scheme + "://" + strings.TrimSpace(r.Host)
	}
	return strings.TrimRight(config.DefaultAPIBaseURL(), "/")
}

func mergedCommandEnv(base []string, overrides map[string]string) []string {
	seen := make(map[string]int, len(base)+len(overrides))
	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		normalized := strings.ToUpper(strings.TrimSpace(key))
		if _, exists := overrides[key]; exists {
			continue
		}
		if idx, ok := seen[normalized]; ok {
			out[idx] = entry
			continue
		}
		seen[normalized] = len(out)
		out = append(out, entry)
	}
	for key, value := range overrides {
		normalized := strings.ToUpper(strings.TrimSpace(key))
		entry := key + "=" + value
		if idx, ok := seen[normalized]; ok {
			out[idx] = entry
			continue
		}
		seen[normalized] = len(out)
		out = append(out, entry)
	}
	return out
}

func commandOutputDetail(stdout, stderr string) string {
	detail := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(stderr), strings.TrimSpace(stdout)}, "\n"))
	if detail == "" {
		return ""
	}
	if len(detail) > larkCLICommandOutputMaxDetail {
		detail = detail[:larkCLICommandOutputMaxDetail] + "..."
	}
	return ": " + detail
}
