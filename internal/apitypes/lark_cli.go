package apitypes

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
	Installed        bool   `json:"installed"`
	LarkCLIPath      string `json:"lark_cli_path,omitempty"`
	ConfigDir        string `json:"config_dir"`
	ConfigPath       string `json:"config_path"`
	SourceConfigPath string `json:"source_config_path,omitempty"`
	RestartStatus    string `json:"restart_status,omitempty"`
	RestartError     string `json:"restart_error,omitempty"`
}
