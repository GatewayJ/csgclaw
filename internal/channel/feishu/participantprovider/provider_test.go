package participantprovider

import (
	"path/filepath"
	"testing"
	"time"

	"csgclaw/internal/agent"
	"csgclaw/internal/apitypes"
	"csgclaw/internal/participant"
)

func TestProviderReadsBotAndAdminConfigFromParticipants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "participants.json")
	store, err := participant.NewStore(path)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	items := []apitypes.Participant{
		{
			ID:              "admin",
			Channel:         participant.ChannelFeishu,
			Type:            participant.TypeHuman,
			Name:            "admin",
			ChannelUserRef:  "ou_admin",
			ChannelUserKind: participant.ChannelUserKindOpenID,
			LifecycleStatus: participant.LifecycleStatusActive,
			Mentionable:     true,
			CreatedAt:       now,
		},
		{
			ID:              agent.ManagerParticipantID,
			Channel:         participant.ChannelFeishu,
			Type:            participant.TypeAgent,
			Name:            agent.ManagerName,
			ChannelUserKind: participant.ChannelUserKindAppID,
			ChannelAppConfig: map[string]any{
				"app_id":     "cli_manager",
				"app_secret": "manager-secret",
			},
			AgentID:         agent.ManagerUserID,
			LifecycleStatus: participant.LifecycleStatusActive,
			Mentionable:     true,
			CreatedAt:       now,
		},
		{
			ID:              "dev",
			Channel:         participant.ChannelFeishu,
			Type:            participant.TypeAgent,
			Name:            "Dev",
			ChannelUserKind: participant.ChannelUserKindAppID,
			ChannelAppConfig: map[string]any{
				"app_id":     "cli_dev",
				"app_secret": "dev-secret",
			},
			AgentID:         "u-dev",
			LifecycleStatus: participant.LifecycleStatusActive,
			Mentionable:     true,
			CreatedAt:       now,
		},
	}
	for _, item := range items {
		if err := store.Save(item); err != nil {
			t.Fatalf("Save(%s) error = %v", item.ID, err)
		}
	}

	provider := New(path)
	participantID, app, ok := provider.BotConfigForAgent(agent.ManagerUserID)
	if !ok {
		t.Fatal("BotConfigForAgent(manager) ok = false, want true")
	}
	if participantID != agent.ManagerParticipantID || app.AppID != "cli_manager" || app.AppSecret != "manager-secret" {
		t.Fatalf("manager config = participant_id=%q app=%+v, want manager app", participantID, app)
	}
	app, ok = provider.BotConfig("dev")
	if !ok || app.AppID != "cli_dev" || app.AppSecret != "dev-secret" {
		t.Fatalf("BotConfig(dev) = %+v, ok=%v; want dev app", app, ok)
	}
	adminOpenID, ok := provider.DefaultAdminOpenID()
	if !ok || adminOpenID != "ou_admin" {
		t.Fatalf("DefaultAdminOpenID() = %q, ok=%v; want ou_admin", adminOpenID, ok)
	}
	adminOpenID, ok = provider.MentionOpenID("admin")
	if !ok || adminOpenID != "ou_admin" {
		t.Fatalf("MentionOpenID(admin) = %q, ok=%v; want ou_admin", adminOpenID, ok)
	}
	if openID, ok := provider.MentionOpenID("dev"); ok || openID != "" {
		t.Fatalf("MentionOpenID(dev) = %q, ok=%v; want no human mention open_id for app-backed agent", openID, ok)
	}
	snapshot := provider.Snapshot()
	if snapshot.AdminOpenID != "ou_admin" || snapshot.Bots[agent.ManagerParticipantID].AppID != "cli_manager" || snapshot.Bots["dev"].AppSecret != "dev-secret" {
		t.Fatalf("Snapshot() = %+v, want participant-backed config", snapshot)
	}
}
