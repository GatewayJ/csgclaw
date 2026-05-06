package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"csgclaw/internal/channel"
	"csgclaw/internal/config"
)

const feishuConfigPathPrefix = "/api/v1/channels/feishu/config/"

type feishuChannelConfigRequest struct {
	AppID       string `json:"app_id"`
	AppSecret   string `json:"app_secret"`
	AdminOpenID string `json:"admin_open_id,omitempty"`
	Reload      *bool  `json:"reload,omitempty"`
}

type feishuChannelConfigResponse struct {
	BotID       string `json:"bot_id"`
	Configured  bool   `json:"configured"`
	AppID       string `json:"app_id,omitempty"`
	AppSecret   string `json:"app_secret"`
	AdminOpenID string `json:"admin_open_id,omitempty"`
	Reloaded    bool   `json:"reloaded,omitempty"`
}

type channelsReloadResponse struct {
	Status     string   `json:"status"`
	FeishuBots []string `json:"feishu_bots"`
}

func (h *Handler) handleChannelsReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.validateServerAccessToken(r.Header.Get("Authorization")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	h.channelConfigMu.Lock()
	defer h.channelConfigMu.Unlock()
	cfg, err := h.reloadChannels()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, channelsReloadResponse{Status: "reloaded", FeishuBots: sortedFeishuBotIDs(cfg.Channels)})
}

func (h *Handler) handleFeishuChannelConfigByBotID(w http.ResponseWriter, r *http.Request) {
	botID, ok := parseFeishuConfigBotID(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !h.validateServerAccessToken(r.Header.Get("Authorization")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetFeishuChannelConfig(w, r, botID)
	case http.MethodPut:
		h.handlePutFeishuChannelConfig(w, r, botID)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleGetFeishuChannelConfig(w http.ResponseWriter, _ *http.Request, botID string) {
	cfg, err := h.loadConfigWithChannelFiles()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app, ok := cfg.Channels.Feishu[botID]
	writeJSON(w, http.StatusOK, maskFeishuChannelConfig(botID, app, ok, cfg.Channels.FeishuAdminOpenID, false))
}

func (h *Handler) handlePutFeishuChannelConfig(w http.ResponseWriter, r *http.Request, botID string) {
	var req feishuChannelConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid feishu channel config request", http.StatusBadRequest)
		return
	}
	req.AppID = strings.TrimSpace(req.AppID)
	req.AppSecret = strings.TrimSpace(req.AppSecret)
	req.AdminOpenID = strings.TrimSpace(req.AdminOpenID)
	if req.AppID == "" {
		http.Error(w, "app_id is required", http.StatusBadRequest)
		return
	}
	if req.AppSecret == "" {
		http.Error(w, "app_secret is required", http.StatusBadRequest)
		return
	}

	h.channelConfigMu.Lock()
	defer h.channelConfigMu.Unlock()

	channels, err := h.loadStandaloneFeishuChannelConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if channels.Feishu == nil {
		channels.Feishu = make(map[string]config.FeishuConfig)
	}
	if req.AdminOpenID != "" {
		channels.FeishuAdminOpenID = req.AdminOpenID
	}
	channels.Feishu[botID] = config.FeishuConfig{AppID: req.AppID, AppSecret: req.AppSecret}

	feishuPath, err := config.FeishuChannelConfigPath(h.configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := config.SaveFeishuChannelConfig(feishuPath, channels); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	reload := true
	if req.Reload != nil {
		reload = *req.Reload
	}
	if reload {
		if _, err := h.reloadChannels(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusOK, maskFeishuChannelConfig(botID, channels.Feishu[botID], true, channels.FeishuAdminOpenID, reload))
}

func (h *Handler) reloadChannels() (config.Config, error) {
	cfg, err := h.loadConfigWithChannelFiles()
	if err != nil {
		return config.Config{}, err
	}
	if h.feishu != nil {
		h.feishu.SetAppConfigs(feishuAppsFromChannels(cfg.Channels))
	}
	if h.svc != nil {
		h.svc.SetChannels(cfg.Channels)
	}
	if h.botSvc != nil {
		h.botSvc.SetDependencies(h.svc, h.im, h.feishu)
	}
	return cfg, nil
}

func (h *Handler) loadConfigWithChannelFiles() (config.Config, error) {
	if strings.TrimSpace(h.configPath) == "" {
		return config.LoadDefaultWithChannelFiles()
	}
	return config.LoadWithChannelFiles(h.configPath)
}

func (h *Handler) loadStandaloneFeishuChannelConfig() (config.ChannelsConfig, error) {
	path, err := config.FeishuChannelConfigPath(h.configPath)
	if err != nil {
		return config.ChannelsConfig{}, err
	}
	channels, ok, err := config.LoadFeishuChannelConfigIfExists(path)
	if err != nil {
		return config.ChannelsConfig{}, err
	}
	if !ok {
		return config.ChannelsConfig{}, nil
	}
	return channels, nil
}

func parseFeishuConfigBotID(path string) (string, bool) {
	if !strings.HasPrefix(path, feishuConfigPathPrefix) {
		return "", false
	}
	botID := strings.TrimSpace(strings.TrimPrefix(path, feishuConfigPathPrefix))
	if err := config.ValidateFeishuChannelBotID(botID); err != nil {
		return "", false
	}
	return botID, true
}

func maskFeishuChannelConfig(botID string, app config.FeishuConfig, configured bool, adminOpenID string, reloaded bool) feishuChannelConfigResponse {
	resp := feishuChannelConfigResponse{
		BotID:       botID,
		Configured:  configured,
		AdminOpenID: strings.TrimSpace(adminOpenID),
		Reloaded:    reloaded,
	}
	if configured {
		resp.AppID = strings.TrimSpace(app.AppID)
		if strings.TrimSpace(app.AppSecret) != "" {
			resp.AppSecret = "present"
		} else {
			resp.AppSecret = "missing"
		}
	} else {
		resp.AppSecret = "missing"
	}
	return resp
}

func feishuAppsFromChannels(cfg config.ChannelsConfig) map[string]channel.FeishuAppConfig {
	apps := make(map[string]channel.FeishuAppConfig, len(cfg.Feishu))
	for name, app := range cfg.Feishu {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		apps[name] = channel.FeishuAppConfig{
			AppID:       app.AppID,
			AppSecret:   app.AppSecret,
			AdminOpenID: cfg.FeishuAdminOpenID,
		}
	}
	return apps
}

func sortedFeishuBotIDs(channels config.ChannelsConfig) []string {
	ids := make([]string, 0, len(channels.Feishu))
	for id := range channels.Feishu {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
