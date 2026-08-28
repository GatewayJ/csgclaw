package apitypes

import "time"

type FeishuBotAppInfo struct {
	AgentID       string `json:"agent_id"`
	ParticipantID string `json:"participant_id"`
	AppID         string `json:"app_id"`
	AppSecret     string `json:"app_secret,omitempty"`
}

type AgentLarkCLIInitResponse struct {
	Status           string `json:"status"`
	AgentID          string `json:"agent_id"`
	ParticipantID    string `json:"participant_id"`
	AppID            string `json:"app_id"`
	LarkCLIPath      string `json:"lark_cli_path,omitempty"`
	ConfigDir        string `json:"config_dir"`
	ConfigPath       string `json:"config_path"`
	SourceConfigPath string `json:"source_config_path,omitempty"`
	RestartStatus    string `json:"restart_status,omitempty"`
	RestartError     string `json:"restart_error,omitempty"`
}

type AgentLarkCLIStatus struct {
	Bound            bool       `json:"bound"`
	Available        bool       `json:"available"`
	State            string     `json:"state"`
	Error            string     `json:"error,omitempty"`
	ExecutablePath   string     `json:"executable_path,omitempty"`
	AppID            string     `json:"app_id,omitempty"`
	ConfigDir        string     `json:"config_dir,omitempty"`
	ConfigPath       string     `json:"config_path,omitempty"`
	SourceConfigPath string     `json:"source_config_path,omitempty"`
	BoundAt          *time.Time `json:"bound_at,omitempty"`
}
