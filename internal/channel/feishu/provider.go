package feishu

// AgentCredentialProvider is the narrow contract needed by sandbox runtimes.
type AgentCredentialProvider interface {
	BotConfigForAgent(agentID string) (participantID string, app AppConfig, ok bool)
}

// Provider is the complete configuration source used by the Feishu channel service.
type Provider interface {
	AgentCredentialProvider
	BotConfig(participantID string) (AppConfig, bool)
	DefaultAdminOpenID() (openID string, ok bool)
	MentionOpenID(participantID string) (openID string, ok bool)
	Snapshot() Snapshot
}
