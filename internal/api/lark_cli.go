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
	"strings"
	"sync"
	"time"

	"csgclaw/internal/agent"
	"csgclaw/internal/apitypes"
	"csgclaw/internal/config"
	"csgclaw/internal/participant"
	"csgclaw/internal/participant/feishubind"
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
	feishuBotNotConfiguredCode    = "feishu_bot_not_configured"
	feishuBotAppIDConflictCode    = "feishu_bot_app_id_conflict"
	larkCLIBindTimeout            = 90 * time.Second
	larkCLIProbeTimeout           = 5 * time.Second
	larkCLIProbeCacheTTL          = 30 * time.Second
	larkCLIExecProviderTimeoutMS  = 10_000
	larkCLIExecProviderMaxBytes   = 64 * 1024
	larkCLICommandOutputMaxDetail = 2000
	larkCLIStatusUnbound          = "unbound"
	larkCLIStatusBound            = "bound"
	larkCLIStatusMismatch         = "mismatch"
	larkCLIStatusUnavailable      = "unavailable"
)

var (
	errFeishuBotNotConfigured = errors.New("feishu bot is not configured")
	errFeishuBotAppIDConflict = feishubind.ErrBotAppIDConflict

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

type larkCLIConfigureError struct {
	status int
	code   string
	err    error
}

type larkCLIProbeCache struct {
	Path      string
	CheckedAt time.Time
	Error     string
}

func (e *larkCLIConfigureError) Error() string {
	if e == nil || e.err == nil {
		return "lark-cli configuration failed"
	}
	return e.err.Error()
}

func (e *larkCLIConfigureError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
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

	result, err := h.configureAgentLarkCLI(r.Context(), target, h.internalSourceBaseURL(), true)
	if err != nil {
		h.writeLarkCLIConfigureError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) configureAgentLarkCLI(
	ctx context.Context,
	target agent.Agent,
	baseURL string,
	restartRuntime bool,
) (apitypes.AgentLarkCLIInitResponse, error) {
	unlock := h.lockAgentLarkCLIBind(target.ID)
	defer unlock()

	appInfo, err := h.feishuBotAppInfoForAgent(target.ID)
	if err != nil {
		return apitypes.AgentLarkCLIInitResponse{}, err
	}
	if err := feishubind.ValidateBotAppIDExclusive(h.participant, target.ID, appInfo.AppID); err != nil {
		return apitypes.AgentLarkCLIInitResponse{}, err
	}
	larkCLIPath, err := ensureLarkCLI(ctx)
	if err != nil {
		return apitypes.AgentLarkCLIInitResponse{}, newLarkCLIConfigureError(
			http.StatusServiceUnavailable, "lark_cli_unavailable", err,
		)
	}
	accessToken, err := h.sourceAccessToken(target.ID)
	if err != nil {
		return apitypes.AgentLarkCLIInitResponse{}, newLarkCLIConfigureError(
			http.StatusServiceUnavailable, "lark_cli_source_auth_unavailable", err,
		)
	}
	layout, err := h.svc.AgentLayout(target.ID)
	if err != nil {
		return apitypes.AgentLarkCLIInitResponse{}, newLarkCLIConfigureError(http.StatusBadRequest, "agent_layout_unavailable", err)
	}
	codexHomeDir := codexHomeDirFromLayout(layout)
	if codexHomeDir == "" {
		return apitypes.AgentLarkCLIInitResponse{}, newLarkCLIConfigureError(
			http.StatusBadRequest, "codex_home_unavailable", errors.New("agent Codex home directory is unavailable"),
		)
	}
	helperPath, err := larkCLISourceHelperPath()
	if err != nil {
		return apitypes.AgentLarkCLIInitResponse{}, newLarkCLIConfigureError(
			http.StatusServiceUnavailable, "lark_cli_source_unavailable", err,
		)
	}

	configDir := filepath.Join(codexHomeDir, larkCLIConfigDirName)
	sourceDir := filepath.Join(codexHomeDir, larkCLISourceDirName)
	sourcePath := filepath.Join(sourceDir, larkCLISourceConfigFileName)
	stagingConfigDir, stagingSourceDir, err := createLarkCLIBindingStagingDirs(codexHomeDir)
	if err != nil {
		return apitypes.AgentLarkCLIInitResponse{}, newLarkCLIConfigureError(
			http.StatusInternalServerError, "lark_cli_config_failed", fmt.Errorf("create lark-cli staging dirs: %w", err),
		)
	}
	defer func() {
		_ = os.RemoveAll(stagingConfigDir)
		_ = os.RemoveAll(stagingSourceDir)
	}()

	stagingSourcePath := filepath.Join(stagingSourceDir, larkCLISourceConfigFileName)
	stagingBindMarkerPath := filepath.Join(stagingSourceDir, larkCLIBindMarkerFileName)
	if err := writeLarkChannelSourceConfig(stagingSourcePath, larkChannelSourceConfig{
		AppID:       appInfo.AppID,
		BaseURL:     baseURL,
		AccessToken: accessToken,
		HelperPath:  helperPath,
		AgentID:     target.ID,
	}); err != nil {
		return apitypes.AgentLarkCLIInitResponse{}, newLarkCLIConfigureError(
			http.StatusInternalServerError, "lark_cli_config_failed", fmt.Errorf("write lark-cli source config: %w", err),
		)
	}
	if err := runLarkCLIConfigBind(ctx, larkCLIPath, stagingConfigDir, stagingSourcePath, codexHomeDir, target.ID); err != nil {
		return apitypes.AgentLarkCLIInitResponse{}, newLarkCLIConfigureError(http.StatusBadGateway, "lark_cli_bind_failed", err)
	}
	stagingConfigPath := filepath.Join(stagingConfigDir, larkCLIWorkspaceName, larkCLIConfigFileName)
	boundAppID, ok := readLarkCLIConfigAppID(stagingConfigPath)
	if !ok || strings.TrimSpace(boundAppID) != strings.TrimSpace(appInfo.AppID) {
		return apitypes.AgentLarkCLIInitResponse{}, newLarkCLIConfigureError(
			http.StatusBadGateway,
			"lark_cli_bind_failed",
			fmt.Errorf("lark-cli config bind did not generate a config for app %q", appInfo.AppID),
		)
	}
	if err := writeLarkCLIBindMarker(stagingBindMarkerPath, larkCLIBindMarker{
		AgentID:          target.ID,
		AppID:            appInfo.AppID,
		ConfigPath:       filepath.Join(configDir, larkCLIWorkspaceName, larkCLIConfigFileName),
		SourceConfigPath: sourcePath,
		BoundAt:          time.Now().UTC(),
	}); err != nil {
		return apitypes.AgentLarkCLIInitResponse{}, newLarkCLIConfigureError(
			http.StatusInternalServerError, "lark_cli_config_failed", fmt.Errorf("write lark-cli bind marker: %w", err),
		)
	}
	if err := replaceLarkCLIBindingDirs(configDir, sourceDir, stagingConfigDir, stagingSourceDir); err != nil {
		return apitypes.AgentLarkCLIInitResponse{}, newLarkCLIConfigureError(
			http.StatusInternalServerError, "lark_cli_config_failed", fmt.Errorf("activate lark-cli binding: %w", err),
		)
	}
	if err := h.refreshAgentInstructionsForLarkCLI(ctx, target); err != nil {
		slog.Warn("refresh lark-cli managed instructions failed", "agent_id", target.ID, "error", err)
	}

	restartStatus := "restart_skipped"
	var restartError string
	if restartRuntime {
		if restarted, err := h.restartAgentCodexRuntimeForLarkCLI(ctx, target.ID); err != nil {
			restartStatus = "restart_failed"
			restartError = err.Error()
			slog.Warn("restart codex worker after lark-cli init failed", "agent_id", target.ID, "error", err)
		} else if restarted {
			restartStatus = "runtime_restarted"
		}
	}

	return apitypes.AgentLarkCLIInitResponse{
		Status:           "configured",
		AgentID:          target.ID,
		ParticipantID:    appInfo.ParticipantID,
		AppID:            appInfo.AppID,
		LarkCLIPath:      larkCLIPath,
		ConfigDir:        configDir,
		ConfigPath:       filepath.Join(configDir, larkCLIWorkspaceName, larkCLIConfigFileName),
		SourceConfigPath: sourcePath,
		RestartStatus:    restartStatus,
		RestartError:     restartError,
	}, nil
}

func newLarkCLIConfigureError(status int, code string, err error) error {
	return &larkCLIConfigureError{status: status, code: strings.TrimSpace(code), err: err}
}

func (h *Handler) writeLarkCLIConfigureError(w http.ResponseWriter, err error) {
	if errors.Is(err, errFeishuBotNotConfigured) || errors.Is(err, errFeishuBotAppIDConflict) {
		h.writeFeishuBotAppInfoError(w, err)
		return
	}
	var configureErr *larkCLIConfigureError
	if errors.As(err, &configureErr) {
		writeCodedAPIError(w, configureErr.status, configureErr.code, configureErr.Error())
		return
	}
	writeAgentOperationError(w, err, http.StatusInternalServerError)
}

func (h *Handler) lockAgentLarkCLIBind(agentID string) func() {
	key := agent.CanonicalID(agentID)
	h.larkCLIBindLocksMu.Lock()
	if h.larkCLIBindLocks == nil {
		h.larkCLIBindLocks = make(map[string]*sync.Mutex)
	}
	lock := h.larkCLIBindLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		h.larkCLIBindLocks[key] = lock
	}
	h.larkCLIBindLocksMu.Unlock()
	lock.Lock()
	return lock.Unlock
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
	unlock := h.lockAgentLarkCLIBind(agentID)
	defer unlock()
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

func (h *Handler) agentLarkCLIStatus(target agent.Agent) *apitypes.AgentLarkCLIStatus {
	if h == nil || h.svc == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(target.RuntimeKind), agent.RuntimeKindCodex) {
		return nil
	}
	larkCLIPath, availabilityErr := h.usableLarkCLI(context.Background())
	available := availabilityErr == nil
	baseStatus := apitypes.AgentLarkCLIStatus{
		Available:      available,
		State:          larkCLIStatusUnbound,
		ExecutablePath: larkCLIPath,
	}
	if availabilityErr != nil {
		baseStatus.Error = availabilityErr.Error()
	}
	layout, err := h.svc.AgentLayout(target.ID)
	if err != nil {
		if !available {
			baseStatus.State = larkCLIStatusUnavailable
		}
		return &baseStatus
	}
	codexHomeDir := codexHomeDirFromLayout(layout)
	if codexHomeDir == "" {
		if !available {
			baseStatus.State = larkCLIStatusUnavailable
		}
		return &baseStatus
	}
	status := readAgentLarkCLIStatus(codexHomeDir, target.ID)
	status.Available = available
	status.ExecutablePath = larkCLIPath
	if availabilityErr != nil {
		status.Error = availabilityErr.Error()
	}
	if status.Bound && h.participant != nil {
		currentAppID := h.feishuBotAppIDForExistingAgent(target)
		if strings.TrimSpace(currentAppID) != "" &&
			strings.TrimSpace(status.AppID) != "" &&
			strings.TrimSpace(currentAppID) != strings.TrimSpace(status.AppID) {
			status.State = larkCLIStatusMismatch
		}
	}
	if !available {
		status.State = larkCLIStatusUnavailable
	}
	return &status
}

func readAgentLarkCLIStatus(codexHomeDir, agentID string) apitypes.AgentLarkCLIStatus {
	configDir := filepath.Join(codexHomeDir, larkCLIConfigDirName)
	configPath := filepath.Join(configDir, larkCLIWorkspaceName, larkCLIConfigFileName)
	sourcePath := filepath.Join(codexHomeDir, larkCLISourceDirName, larkCLISourceConfigFileName)
	markerPath := filepath.Join(codexHomeDir, larkCLISourceDirName, larkCLIBindMarkerFileName)
	status := apitypes.AgentLarkCLIStatus{
		State:            larkCLIStatusUnbound,
		ConfigDir:        configDir,
		ConfigPath:       configPath,
		SourceConfigPath: sourcePath,
	}
	marker, ok := readLarkCLIBindMarker(markerPath)
	if !ok {
		return status
	}
	sourceAppID, ok := readLarkChannelSourceAppID(sourcePath)
	if !ok {
		status.State = larkCLIStatusMismatch
		status.Error = "lark-cli source config is missing or invalid"
		return status
	}
	status.AppID = strings.TrimSpace(marker.AppID)
	if status.AppID == "" {
		status.AppID = strings.TrimSpace(sourceAppID)
	}
	if !marker.BoundAt.IsZero() {
		boundAt := marker.BoundAt.UTC()
		status.BoundAt = &boundAt
	}
	if markerConfigPath := strings.TrimSpace(marker.ConfigPath); markerConfigPath != "" && markerConfigPath != configPath {
		status.State = larkCLIStatusMismatch
		status.Error = "lark-cli bind marker points to a different config file"
		return status
	}
	if markerSourcePath := strings.TrimSpace(marker.SourceConfigPath); markerSourcePath != "" && markerSourcePath != sourcePath {
		status.State = larkCLIStatusMismatch
		status.Error = "lark-cli bind marker points to a different source config file"
		return status
	}
	if strings.TrimSpace(marker.AppID) != "" &&
		strings.TrimSpace(sourceAppID) != "" &&
		strings.TrimSpace(marker.AppID) != strings.TrimSpace(sourceAppID) {
		status.State = larkCLIStatusMismatch
		status.Error = "lark-cli bind marker and source config use different app IDs"
		return status
	}
	if expectedAgentID := agent.CanonicalID(agentID); expectedAgentID != "" {
		markerAgentID := agent.CanonicalID(marker.AgentID)
		if markerAgentID != "" && markerAgentID != expectedAgentID {
			status.State = larkCLIStatusMismatch
			status.Error = "lark-cli bind marker belongs to a different worker"
			return status
		}
	}
	configAppID, ok := readLarkCLIConfigAppID(configPath)
	if !ok {
		status.State = larkCLIStatusMismatch
		status.Error = "lark-cli config file is missing or invalid"
		return status
	}
	if strings.TrimSpace(configAppID) != strings.TrimSpace(sourceAppID) {
		status.State = larkCLIStatusMismatch
		status.Error = "lark-cli config and source config use different app IDs"
		return status
	}
	status.Bound = true
	status.State = larkCLIStatusBound
	return status
}

func readLarkCLIBindMarker(path string) (larkCLIBindMarker, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return larkCLIBindMarker{}, false
	}
	var marker larkCLIBindMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return larkCLIBindMarker{}, false
	}
	return marker, true
}

func readLarkChannelSourceAppID(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var payload struct {
		Accounts struct {
			App struct {
				ID string `json:"id"`
			} `json:"app"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false
	}
	appID := strings.TrimSpace(payload.Accounts.App.ID)
	return appID, appID != ""
}

func readLarkCLIConfigAppID(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var payload struct {
		Apps []struct {
			AppID string `json:"appId"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || len(payload.Apps) == 0 {
		return "", false
	}
	appID := strings.TrimSpace(payload.Apps[0].AppID)
	return appID, appID != ""
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
	appID, ok := feishuBotAppIDFromParticipant(agentID, item)
	if !ok {
		return feishuBotAppInfo{}, false
	}
	appSecret := channelAppConfigString(item.ChannelAppConfig, participant.ChannelAppConfigAppSecretKey)
	if appSecret == "" || appSecret == participant.RedactedSecretValue {
		return feishuBotAppInfo{}, false
	}
	return feishuBotAppInfo{
		AgentID:       strings.TrimSpace(agentID),
		ParticipantID: strings.TrimSpace(item.ID),
		AppID:         appID,
		AppSecret:     appSecret,
	}, true
}

func (h *Handler) feishuBotAppIDForExistingAgent(target agent.Agent) string {
	if h == nil || h.participant == nil {
		return ""
	}
	canonicalParticipantID := agent.ParticipantIDForAgent(target.Name, target.ID)
	if item, ok := h.participant.Get(participant.ChannelFeishu, canonicalParticipantID); ok {
		if appID, ok := feishuBotAppIDFromParticipant(target.ID, item); ok {
			return appID
		}
	}
	for _, item := range h.participant.List(participant.ListOptions{
		Channel: participant.ChannelFeishu,
		Type:    participant.TypeAgent,
		AgentID: target.ID,
	}) {
		if appID, ok := feishuBotAppIDFromParticipant(target.ID, item); ok {
			return appID
		}
	}
	return ""
}

func feishuBotAppIDFromParticipant(agentID string, item apitypes.Participant) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(item.Channel), participant.ChannelFeishu) ||
		strings.TrimSpace(item.Type) != participant.TypeAgent ||
		strings.TrimSpace(item.AgentID) != strings.TrimSpace(agentID) {
		return "", false
	}
	appID := channelAppConfigString(item.ChannelAppConfig, "app_id")
	return appID, appID != ""
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

func ensureLarkCLI(ctx context.Context) (string, error) {
	path, err := findLarkCLI()
	if err != nil {
		return "", err
	}
	if err := probeLarkCLI(ctx, path); err != nil {
		return path, err
	}
	return path, nil
}

func findLarkCLI() (string, error) {
	path, err := larkCLILookPath("lark-cli")
	path = strings.TrimSpace(path)
	if err != nil || path == "" {
		return "", fmt.Errorf("lark-cli is not installed or not on PATH. Install lark-cli on this host and retry")
	}
	return path, nil
}

func probeLarkCLI(ctx context.Context, path string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, larkCLIProbeTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := larkCLICommandContext(probeCtx, path, "-v")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("lark-cli at %q cannot be started: %w%s", path, err, commandOutputDetail(stdout.String(), stderr.String()))
	}
	return nil
}

func (h *Handler) usableLarkCLI(ctx context.Context) (string, error) {
	path, err := findLarkCLI()
	if err != nil {
		return path, err
	}
	if h == nil {
		return path, probeLarkCLI(ctx, path)
	}
	now := time.Now()
	h.larkCLIProbeMu.Lock()
	defer h.larkCLIProbeMu.Unlock()
	if h.larkCLIProbe.Path == path && now.Sub(h.larkCLIProbe.CheckedAt) < larkCLIProbeCacheTTL {
		if h.larkCLIProbe.Error != "" {
			return path, errors.New(h.larkCLIProbe.Error)
		}
		return path, nil
	}
	err = probeLarkCLI(ctx, path)
	h.larkCLIProbe = larkCLIProbeCache{Path: path, CheckedAt: now}
	if err != nil {
		h.larkCLIProbe.Error = err.Error()
	}
	return path, err
}

func createLarkCLIBindingStagingDirs(codexHomeDir string) (string, string, error) {
	if err := os.MkdirAll(codexHomeDir, 0o700); err != nil {
		return "", "", fmt.Errorf("create Codex home dir: %w", err)
	}
	configDir, err := os.MkdirTemp(codexHomeDir, "."+larkCLIConfigDirName+"-*")
	if err != nil {
		return "", "", fmt.Errorf("create config staging dir: %w", err)
	}
	_ = os.Chmod(configDir, 0o700)
	sourceDir, err := os.MkdirTemp(codexHomeDir, "."+larkCLISourceDirName+"-*")
	if err != nil {
		_ = os.RemoveAll(configDir)
		return "", "", fmt.Errorf("create source staging dir: %w", err)
	}
	_ = os.Chmod(sourceDir, 0o700)
	return configDir, sourceDir, nil
}

func replaceLarkCLIBindingDirs(configDir, sourceDir, stagingConfigDir, stagingSourceDir string) error {
	if err := replacePathByRemoveAndRename(configDir, stagingConfigDir); err != nil {
		return fmt.Errorf("activate lark-cli config dir: %w", err)
	}
	if err := replacePathByRemoveAndRename(sourceDir, stagingSourceDir); err != nil {
		return fmt.Errorf("activate lark-cli source dir: %w", err)
	}
	_ = os.Chmod(configDir, 0o700)
	_ = os.Chmod(sourceDir, 0o700)
	return nil
}

func replacePathByRemoveAndRename(target, staged string) error {
	parent := filepath.Dir(target)
	if parent == "." || parent == "" {
		return fmt.Errorf("target parent is unavailable")
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create target parent: %w", err)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove existing target: %w", err)
	}
	if err := os.Rename(staged, target); err != nil {
		return err
	}
	return nil
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

func (h *Handler) internalSourceBaseURL() string {
	if h != nil && strings.TrimSpace(h.internalBaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(h.internalBaseURL), "/")
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
