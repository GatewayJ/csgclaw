package feishu

type BotCredentialProvider interface {
	BotConfig(participantID string) (AppConfig, bool)
}

type AgentCredentialProvider interface {
	BotConfigForAgent(agentID string) (participantID string, app AppConfig, ok bool)
}

type DefaultAdminOpenIDProvider interface {
	DefaultAdminOpenID() (openID string, ok bool)
}

type ParticipantMentionProvider interface {
	MentionOpenID(participantID string) (openID string, ok bool)
}

type SnapshotProvider interface {
	Snapshot() Snapshot
}

type Provider interface {
	BotCredentialProvider
	SnapshotProvider
}
