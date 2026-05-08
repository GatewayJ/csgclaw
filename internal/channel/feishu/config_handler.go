package feishu

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"

	"csgclaw/internal/agent"
	"csgclaw/internal/bot"
	channelroot "csgclaw/internal/channel"
	"csgclaw/internal/config"
	"csgclaw/internal/im"
)

const ConfigAPIPath = "/api/v1/channels/feishu/config"

type ConfigHandlerOptions struct {
	AgentService        *agent.Service
	BotService          *bot.Service
	IMService           *im.Service
	FeishuService       *channelroot.FeishuService
	ConfigPath          string
	ValidateAccessToken func(string) bool
}

type ConfigHandler struct {
	mu                  sync.Mutex
	agentSvc            *agent.Service
	botSvc              *bot.Service
	im                  *im.Service
	feishu              *channelroot.FeishuService
	configPath          string
	validateAccessToken func(string) bool
}

type ConfigRequest struct {
	BotID       string `json:"bot_id,omitempty"`
	AppID       string `json:"app_id"`
	AppSecret   string `json:"app_secret"`
	AdminOpenID string `json:"admin_open_id,omitempty"`
	Reload      *bool  `json:"reload,omitempty"`
}

type ConfigResponse struct {
	BotID       string `json:"bot_id"`
	Configured  bool   `json:"configured"`
	AppID       string `json:"app_id,omitempty"`
	AppSecret   string `json:"app_secret"`
	AdminOpenID string `json:"admin_open_id,omitempty"`
	Reloaded    bool   `json:"reloaded,omitempty"`
}

type ReloadResponse struct {
	Status     string   `json:"status"`
	FeishuBots []string `json:"feishu_bots"`
}

func NewConfigHandler(opts ConfigHandlerOptions) *ConfigHandler {
	return &ConfigHandler{
		agentSvc:            opts.AgentService,
		botSvc:              opts.BotService,
		im:                  opts.IMService,
		feishu:              opts.FeishuService,
		configPath:          strings.TrimSpace(opts.ConfigPath),
		validateAccessToken: opts.ValidateAccessToken,
	}
}

func (h *ConfigHandler) SetConfigPath(path string) {
	if h == nil {
		return
	}
	h.configPath = strings.TrimSpace(path)
}

func (h *ConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil {
		http.Error(w, "feishu config handler is not configured", http.StatusServiceUnavailable)
		return
	}
	if !h.authorized(r.Header.Get("Authorization")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch {
	case r.URL.Path == ConfigAPIPath:
		h.handleConfig(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *ConfigHandler) authorized(authHeader string) bool {
	if h.validateAccessToken == nil {
		return true
	}
	return h.validateAccessToken(authHeader)
}

func (h *ConfigHandler) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		botID, ok := h.botIDFromRequest(r, "")
		if !ok {
			http.Error(w, "bot_id is required", http.StatusBadRequest)
			return
		}
		h.handleGet(w, botID)
	case http.MethodPut:
		h.handlePut(w, r)
	case http.MethodPost:
		h.handleReload(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *ConfigHandler) handleGet(w http.ResponseWriter, botID string) {
	cfg, err := h.loadConfigWithChannelFiles()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app, ok := cfg.Channels.Feishu[botID]
	writeJSON(w, http.StatusOK, MaskConfig(botID, app, ok, cfg.Channels.FeishuAdminOpenID, false))
}

func (h *ConfigHandler) handlePut(w http.ResponseWriter, r *http.Request) {
	var req ConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid feishu channel config request", http.StatusBadRequest)
		return
	}
	botID, ok := h.botIDFromRequest(r, req.BotID)
	if !ok {
		http.Error(w, "bot_id is required", http.StatusBadRequest)
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

	h.mu.Lock()
	defer h.mu.Unlock()

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
	writeJSON(w, http.StatusOK, MaskConfig(botID, channels.Feishu[botID], true, channels.FeishuAdminOpenID, reload))
}

func (h *ConfigHandler) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	cfg, err := h.reloadChannels()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, ReloadResponse{Status: "reloaded", FeishuBots: sortedBotIDs(cfg.Channels)})
}

func (h *ConfigHandler) botIDFromRequest(r *http.Request, bodyBotID string) (string, bool) {
	botID := strings.TrimSpace(bodyBotID)
	if botID == "" {
		botID = strings.TrimSpace(r.URL.Query().Get("bot_id"))
	}
	if err := config.ValidateFeishuChannelBotID(botID); err != nil {
		return "", false
	}
	return botID, true
}

func (h *ConfigHandler) reloadChannels() (config.Config, error) {
	cfg, err := h.loadConfigWithChannelFiles()
	if err != nil {
		return config.Config{}, err
	}
	if h.feishu != nil {
		h.feishu.SetAppConfigs(AppsFromChannels(cfg.Channels))
	}
	if h.agentSvc != nil {
		h.agentSvc.SetChannels(cfg.Channels)
	}
	if h.botSvc != nil {
		h.botSvc.SetDependencies(h.agentSvc, h.im, h.feishu)
	}
	return cfg, nil
}

func (h *ConfigHandler) loadConfigWithChannelFiles() (config.Config, error) {
	if strings.TrimSpace(h.configPath) == "" {
		return config.LoadDefaultWithChannelFiles()
	}
	return config.LoadWithChannelFiles(h.configPath)
}

func (h *ConfigHandler) loadStandaloneFeishuChannelConfig() (config.ChannelsConfig, error) {
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

func MaskConfig(botID string, app config.FeishuConfig, configured bool, adminOpenID string, reloaded bool) ConfigResponse {
	resp := ConfigResponse{
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

func AppsFromChannels(cfg config.ChannelsConfig) map[string]channelroot.FeishuAppConfig {
	apps := make(map[string]channelroot.FeishuAppConfig, len(cfg.Feishu))
	for name, app := range cfg.Feishu {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		apps[name] = channelroot.FeishuAppConfig{
			AppID:       app.AppID,
			AppSecret:   app.AppSecret,
			AdminOpenID: cfg.FeishuAdminOpenID,
		}
	}
	return apps
}

func sortedBotIDs(channels config.ChannelsConfig) []string {
	ids := make([]string, 0, len(channels.Feishu))
	for id := range channels.Feishu {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
