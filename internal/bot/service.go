package bot

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"csgclaw/internal/agent"
	"csgclaw/internal/channel"
	"csgclaw/internal/im"
)

type Service struct {
	store  *Store
	agents *agent.Service
	im     *im.Service
	feishu *channel.FeishuService
}

func NewService(store *Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("bot store is required")
	}
	return &Service{store: store}, nil
}

func NewServiceWithDependencies(store *Store, agentSvc *agent.Service, imSvc *im.Service, feishuSvc ...*channel.FeishuService) (*Service, error) {
	s, err := NewService(store)
	if err != nil {
		return nil, err
	}
	s.SetDependencies(agentSvc, imSvc, feishuSvc...)
	return s, nil
}

func (s *Service) SetDependencies(agentSvc *agent.Service, imSvc *im.Service, feishuSvc ...*channel.FeishuService) {
	if s == nil {
		return
	}
	s.agents = agentSvc
	s.im = imSvc
	if len(feishuSvc) > 0 {
		s.feishu = feishuSvc[0]
	}
}

func (s *Service) List(channel, role string) ([]Bot, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("bot store is required")
	}

	all := s.store.List()
	if strings.TrimSpace(channel) == "" && strings.TrimSpace(role) == "" {
		return all, nil
	}

	normalizedChannel := ""
	if strings.TrimSpace(channel) != "" {
		normalized, err := NormalizeChannel(channel)
		if err != nil {
			return nil, err
		}
		normalizedChannel = string(normalized)
	}

	normalizedRole := ""
	if strings.TrimSpace(role) != "" {
		normalized, err := NormalizeRole(role)
		if err != nil {
			return nil, err
		}
		normalizedRole = string(normalized)
	}

	filtered := make([]Bot, 0, len(all))
	for _, b := range all {
		if normalizedChannel != "" && b.Channel != normalizedChannel {
			continue
		}
		if normalizedRole != "" && b.Role != normalizedRole {
			continue
		}
		filtered = append(filtered, b)
	}
	if normalizedChannel == string(ChannelFeishu) {
		var err error
		filtered, err = s.appendConfiguredFeishuBots(context.Background(), filtered, normalizedRole)
		if err != nil {
			return nil, err
		}
	}
	return filtered, nil
}

func (s *Service) appendConfiguredFeishuBots(ctx context.Context, bots []Bot, role string) ([]Bot, error) {
	if s.feishu == nil {
		return bots, nil
	}
	apps := s.feishu.AppConfigs()
	if len(apps) == 0 {
		return bots, nil
	}

	seen := make(map[string]struct{}, len(bots))
	for _, b := range bots {
		id := strings.TrimSpace(b.ID)
		seen[id] = struct{}{}
		seen[configuredFeishuBotDisplayID(id)] = struct{}{}
	}

	configuredIDs := make([]string, 0, len(apps))
	for id := range apps {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		configuredIDs = append(configuredIDs, id)
	}
	slices.Sort(configuredIDs)

	for _, id := range configuredIDs {
		displayID := configuredFeishuBotDisplayID(id)
		if displayID == "" {
			continue
		}
		if _, ok := seen[displayID]; ok {
			continue
		}
		botRole := string(RoleWorker)
		if id == agent.ManagerUserID {
			botRole = string(RoleManager)
		}
		if role != "" && botRole != role {
			continue
		}
		agentID := ""
		available := false
		if s.agents != nil {
			if _, ok := s.agents.Agent(id); ok {
				agentID = id
				available = true
			}
		}
		openID, appName, err := s.feishu.ResolveBotOpenID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("resolve configured feishu bot %q open_id: %w", id, err)
		}
		name := strings.TrimSpace(appName)
		if name == "" {
			name = displayID
		}
		bots = append(bots, Bot{
			ID:        id,
			Name:      name,
			Role:      botRole,
			Channel:   string(ChannelFeishu),
			AgentID:   agentID,
			UserID:    strings.TrimSpace(openID),
			Available: available,
		})
		seen[displayID] = struct{}{}
	}
	return bots, nil
}

func configuredFeishuBotDisplayID(id string) string {
	id = strings.TrimSpace(id)
	return strings.TrimPrefix(id, "u-")
}

func (s *Service) Delete(ctx context.Context, channel, id string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("bot store is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("bot id is required")
	}
	if strings.TrimSpace(channel) == "" {
		channel = string(ChannelCSGClaw)
	}
	deleted, ok, err := s.store.DeleteByChannelID(channel, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bot %q not found", id)
	}
	if s.agents == nil {
		return nil
	}
	if strings.TrimSpace(deleted.Role) != string(RoleWorker) {
		return nil
	}
	agentID := strings.TrimSpace(deleted.AgentID)
	if agentID == "" {
		return nil
	}
	for _, b := range s.store.List() {
		if strings.TrimSpace(b.AgentID) == agentID {
			return nil
		}
	}
	if err := s.agents.Delete(ctx, agentID); err != nil {
		return fmt.Errorf("delete backing agent %q: %w", agentID, err)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, req CreateRequest) (Bot, error) {
	if s == nil || s.store == nil {
		return Bot{}, fmt.Errorf("bot store is required")
	}

	normalized, err := NormalizeCreateRequest(req)
	if err != nil {
		return Bot{}, err
	}
	if s.agents == nil {
		return Bot{}, fmt.Errorf("agent service is required")
	}
	switch normalized.Role {
	case string(RoleManager):
		return s.createManager(ctx, normalized, false)
	case string(RoleWorker):
		return s.createWorker(ctx, normalized)
	default:
		return Bot{}, fmt.Errorf("role must be one of %q or %q", RoleManager, RoleWorker)
	}
}

func (s *Service) CreateManager(ctx context.Context, req CreateRequest, forceRecreateAgent bool) (Bot, error) {
	if s == nil || s.store == nil {
		return Bot{}, fmt.Errorf("bot store is required")
	}
	req.Role = string(RoleManager)
	normalized, err := NormalizeCreateRequest(req)
	if err != nil {
		return Bot{}, err
	}
	if s.agents == nil {
		return Bot{}, fmt.Errorf("agent service is required")
	}
	return s.createManager(ctx, normalized, forceRecreateAgent)
}

func (s *Service) createWorker(ctx context.Context, normalized CreateRequest) (Bot, error) {
	created, ok := s.agents.Agent(workerAgentID(normalized))
	if ok {
		if strings.ToLower(strings.TrimSpace(created.Role)) != agent.RoleWorker {
			return Bot{}, fmt.Errorf("agent id %q already exists with role %q", created.ID, created.Role)
		}
	} else {
		var err error
		created, err = s.agents.CreateWorker(ctx, agent.CreateRequest{
			ID:          normalized.ID,
			Name:        normalized.Name,
			Description: normalized.Description,
			Role:        agent.RoleWorker,
			ModelID:     normalized.ModelID,
			RuntimeMode: normalized.RuntimeMode,
		})
		if err != nil {
			return Bot{}, err
		}
	}

	userID, userCreatedAt, err := s.ensureChannelUser(normalized.Channel, created)
	if err != nil {
		// TODO: compensate by deleting the agent/box created above once agent deletion
		// semantics are safe to call from bot creation.
		return Bot{}, err
	}

	createdAt := userCreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = created.CreatedAt.UTC()
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	b := Bot{
		ID:          created.ID,
		Name:        created.Name,
		Description: normalized.Description,
		Role:        string(RoleWorker),
		Channel:     normalized.Channel,
		AgentID:     created.ID,
		UserID:      userID,
		Available:   true,
		CreatedAt:   createdAt,
	}
	if _, ok, err := s.store.GetByChannelID(b.Channel, b.ID); err != nil {
		return Bot{}, err
	} else if ok {
		if err := s.store.Save(b); err != nil {
			return Bot{}, err
		}
		return b, nil
	}
	if err := s.store.Save(b); err != nil {
		return Bot{}, err
	}
	return b, nil
}

func (s *Service) createManager(ctx context.Context, normalized CreateRequest, forceRecreateAgent bool) (Bot, error) {
	if normalized.ID != "" && normalized.ID != agent.ManagerUserID {
		return Bot{}, fmt.Errorf("manager bot id must be %q", agent.ManagerUserID)
	}

	manager, ok := s.agents.Agent(agent.ManagerUserID)
	if forceRecreateAgent || !ok || strings.ToLower(strings.TrimSpace(manager.Role)) != agent.RoleManager {
		ensured, err := s.agents.EnsureManager(ctx, forceRecreateAgent)
		if err != nil {
			return Bot{}, err
		}
		manager = ensured
	}

	userID, userCreatedAt, err := s.ensureChannelUser(normalized.Channel, manager)
	if err != nil {
		return Bot{}, err
	}

	createdAt := userCreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = manager.CreatedAt.UTC()
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	b := Bot{
		ID:          manager.ID,
		Name:        normalized.Name,
		Description: normalized.Description,
		Role:        string(RoleManager),
		Channel:     normalized.Channel,
		AgentID:     manager.ID,
		UserID:      userID,
		Available:   true,
		CreatedAt:   createdAt,
	}
	if _, ok, err := s.store.GetByChannelID(b.Channel, b.ID); err != nil {
		return Bot{}, err
	} else if ok {
		if err := s.store.Save(b); err != nil {
			return Bot{}, err
		}
		return b, nil
	}
	if err := s.store.Save(b); err != nil {
		return Bot{}, err
	}
	return b, nil
}

func workerAgentID(req CreateRequest) string {
	if id := strings.TrimSpace(req.ID); id != "" {
		return id
	}
	return fmt.Sprintf("u-%s", strings.TrimSpace(req.Name))
}

func (s *Service) ensureChannelUser(channelName string, created agent.Agent) (string, time.Time, error) {
	switch channelName {
	case string(ChannelCSGClaw):
		if s.im == nil {
			return "", time.Time{}, fmt.Errorf("im service is required")
		}
		user, _, err := s.im.EnsureAgentUser(im.EnsureAgentUserRequest{
			ID:     created.ID,
			Name:   created.Name,
			Handle: deriveAgentHandle(created),
			Role:   displayRole(created.Role),
		})
		if err != nil {
			return "", time.Time{}, fmt.Errorf("failed to ensure im user: %w", err)
		}
		return user.ID, user.CreatedAt, nil
	case string(ChannelFeishu):
		if s.feishu == nil {
			return "", time.Time{}, fmt.Errorf("feishu service is required")
		}
		user, err := s.feishu.EnsureUser(channel.FeishuCreateUserRequest{
			ID:     created.ID,
			Name:   created.Name,
			Handle: deriveAgentHandle(created),
			Role:   displayRole(created.Role),
		})
		if err != nil {
			return "", time.Time{}, fmt.Errorf("failed to ensure feishu user: %w", err)
		}
		return user.ID, user.CreatedAt, nil
	default:
		return "", time.Time{}, fmt.Errorf("channel must be one of %q or %q", ChannelCSGClaw, ChannelFeishu)
	}
}

func deriveAgentHandle(a agent.Agent) string {
	if handle, ok := sanitizeHandle(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(a.Name), " ", "-"))); ok {
		return handle
	}
	if handle, ok := sanitizeHandle(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(a.ID), "u-"))); ok {
		return handle
	}
	switch strings.ToLower(strings.TrimSpace(a.Role)) {
	case agent.RoleManager:
		return "manager"
	case agent.RoleWorker:
		return "worker"
	default:
		return "agent"
	}
}

func displayRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case agent.RoleManager:
		return "manager"
	case agent.RoleWorker:
		return "Worker"
	default:
		return "Agent"
	}
}

func sanitizeHandle(input string) (string, bool) {
	var b strings.Builder
	hasAlphaNum := false
	for _, r := range input {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			hasAlphaNum = true
			b.WriteRune(r)
			continue
		}
		if r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 || !hasAlphaNum {
		return "", false
	}
	return b.String(), true
}
