package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	feishuconfig "csgclaw/internal/channel/feishu"
	"csgclaw/internal/config"
	"csgclaw/internal/im"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	feishuManagerBotID = "u-manager"
)

type FeishuCreateUserRequest struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	Handle string `json:"handle,omitempty"`
	Role   string `json:"role,omitempty"`
	Avatar string `json:"avatar,omitempty"`
}

type FeishuAppConfig struct {
	AppID       string
	AppSecret   string
	AdminOpenID string
}

type FeishuBotInfo struct {
	OpenID  string
	AppName string
}

type FeishuCreateChatRequest struct {
	Title       string
	Description string
	CreatorID   string
	MemberIDs   []string
}

type FeishuCreateChatResponse struct {
	ChatID      string
	Name        string
	Description string
}

type FeishuCreateChatFunc func(context.Context, FeishuAppConfig, FeishuCreateChatRequest) (FeishuCreateChatResponse, error)

type FeishuAddChatMembersRequest struct {
	ChatID       string
	MemberIDs    []string
	MemberAppIDs []string
}

type FeishuAddChatMembersFunc func(context.Context, FeishuAppConfig, FeishuAddChatMembersRequest) error

type FeishuListChatMembersFunc func(context.Context, FeishuAppConfig, map[string]FeishuAppConfig, string) ([]im.User, error)

type FeishuListChatsFunc func(context.Context, FeishuAppConfig) ([]im.Room, error)

type FeishuListRoomMessagesFunc func(context.Context, FeishuAppConfig, string) ([]im.Message, error)

type FeishuDeleteChatFunc func(context.Context, FeishuAppConfig, string) error

type FeishuSendMessageRequest struct {
	ChatID           string
	Content          string
	UUID             string
	MentionID        string
	MentionAppConfig FeishuAppConfig
}

type FeishuSendMessageResponse struct {
	MessageID     string
	SenderOpenID  string
	MentionOpenID string
}

type FeishuSendMessageFunc func(context.Context, FeishuAppConfig, FeishuSendMessageRequest) (FeishuSendMessageResponse, error)

type FeishuConfigReloadHook func(config.ChannelsConfig)

type FeishuService struct {
	mu               sync.RWMutex
	users            map[string]im.User
	byHandle         map[string]string
	rooms            map[string]*im.Room
	apps             map[string]FeishuAppConfig
	resolveBotInfo   func(context.Context, FeishuAppConfig) (FeishuBotInfo, error)
	createChat       FeishuCreateChatFunc
	addChatMembers   FeishuAddChatMembersFunc
	listChatMembers  FeishuListChatMembersFunc
	listChats        FeishuListChatsFunc
	listRoomMessages FeishuListRoomMessagesFunc
	deleteChat       FeishuDeleteChatFunc
	sendMessage      FeishuSendMessageFunc
	messageBus       *FeishuMessageBus
	configStore      *feishuconfig.Config
	configReloadHook FeishuConfigReloadHook
}

func NewFeishuService(apps ...map[string]FeishuAppConfig) *FeishuService {
	configuredApps := make(map[string]FeishuAppConfig)
	if len(apps) > 0 {
		for name, app := range apps[0] {
			configuredApps[name] = app
		}
	}
	return &FeishuService{
		users:            make(map[string]im.User),
		byHandle:         make(map[string]string),
		rooms:            make(map[string]*im.Room),
		apps:             configuredApps,
		resolveBotInfo:   fetchBotInfo,
		createChat:       defaultFeishuCreateChat,
		addChatMembers:   defaultFeishuAddChatMembers,
		listChatMembers:  defaultFeishuListChatMembers,
		listChats:        defaultFeishuListChats,
		listRoomMessages: defaultFeishuListRoomMessages,
		deleteChat:       defaultFeishuDeleteChat,
		sendMessage:      defaultFeishuSendMessage,
		messageBus:       NewFeishuMessageBus(),
		configStore:      feishuconfig.NewConfig(""),
	}
}

func NewFeishuServiceWithBotOpenIDResolver(apps map[string]FeishuAppConfig, resolveBotInfo func(context.Context, FeishuAppConfig) (FeishuBotInfo, error)) *FeishuService {
	svc := NewFeishuService(apps)
	if resolveBotInfo != nil {
		svc.resolveBotInfo = resolveBotInfo
	}
	return svc
}

func NewFeishuServiceWithCreateChat(apps map[string]FeishuAppConfig, createChat FeishuCreateChatFunc) *FeishuService {
	svc := NewFeishuService(apps)
	if createChat != nil {
		svc.createChat = createChat
	}
	return svc
}

func NewFeishuServiceWithCreateChatAndAddMembers(apps map[string]FeishuAppConfig, createChat FeishuCreateChatFunc, addChatMembers FeishuAddChatMembersFunc, listChatMembers ...FeishuListChatMembersFunc) *FeishuService {
	svc := NewFeishuServiceWithCreateChat(apps, createChat)
	if addChatMembers != nil {
		svc.addChatMembers = addChatMembers
	}
	if len(listChatMembers) > 0 && listChatMembers[0] != nil {
		svc.listChatMembers = listChatMembers[0]
	}
	return svc
}

func NewFeishuServiceWithListRoomMessages(apps map[string]FeishuAppConfig, listRoomMessages FeishuListRoomMessagesFunc) *FeishuService {
	svc := NewFeishuService(apps)
	if listRoomMessages != nil {
		svc.listRoomMessages = listRoomMessages
	}
	return svc
}

func NewFeishuServiceWithDeleteChat(apps map[string]FeishuAppConfig, deleteChat FeishuDeleteChatFunc) *FeishuService {
	svc := NewFeishuService(apps)
	if deleteChat != nil {
		svc.deleteChat = deleteChat
	}
	return svc
}

func NewFeishuServiceWithCreateChatAndListRoomMessages(apps map[string]FeishuAppConfig, createChat FeishuCreateChatFunc, listRoomMessages FeishuListRoomMessagesFunc) *FeishuService {
	svc := NewFeishuServiceWithCreateChat(apps, createChat)
	if listRoomMessages != nil {
		svc.listRoomMessages = listRoomMessages
	}
	return svc
}

func NewFeishuServiceWithSendMessage(apps map[string]FeishuAppConfig, sendMessage FeishuSendMessageFunc) *FeishuService {
	svc := NewFeishuService(apps)
	if sendMessage != nil {
		svc.sendMessage = sendMessage
	}
	return svc
}

func (s *FeishuService) MessageBus() *FeishuMessageBus {
	if s == nil {
		return nil
	}
	return s.messageBus
}

func (s *FeishuService) AppConfigs() map[string]FeishuAppConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return cloneFeishuAppConfigs(s.apps)
}

func (s *FeishuService) SetAppConfigs(apps map[string]FeishuAppConfig) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apps = cloneFeishuAppConfigs(apps)
}

func cloneFeishuAppConfigs(apps map[string]FeishuAppConfig) map[string]FeishuAppConfig {
	cloned := make(map[string]FeishuAppConfig, len(apps))
	for name, app := range apps {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		cloned[name] = app
	}
	return cloned
}

func (s *FeishuService) CreateUser(req FeishuCreateUserRequest) (im.User, error) {
	// Mock implementation. Real Feishu support should call the external Feishu API.
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return im.User{}, fmt.Errorf("name is required")
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = fmt.Sprintf("fsu-%d", time.Now().UnixNano())
	}
	handle := strings.ToLower(strings.TrimSpace(req.Handle))
	if handle == "" {
		handle = deriveHandle(name, id)
	}
	role := strings.ToLower(strings.TrimSpace(req.Role))
	if role == "" {
		role = "member"
	}
	avatar := strings.TrimSpace(req.Avatar)
	if avatar == "" {
		avatar = initials(name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.users[id]; ok {
		return im.User{}, fmt.Errorf("user already exists")
	}
	if existingID, ok := s.byHandle[handle]; ok && existingID != id {
		return im.User{}, fmt.Errorf("handle %q already exists", handle)
	}

	user := im.User{
		ID:        id,
		Name:      name,
		Handle:    handle,
		Role:      role,
		Avatar:    avatar,
		IsOnline:  true,
		AccentHex: accentHexForID(id),
		CreatedAt: time.Now().UTC(),
	}
	s.users[id] = user
	s.byHandle[handle] = id
	return user, nil
}

func (s *FeishuService) ListUsers() []im.User {
	s.mu.RLock()
	apps := make(map[string]FeishuAppConfig, len(s.apps))
	for botID, app := range s.apps {
		apps[botID] = app
	}
	localUsers := make(map[string]im.User, len(s.users))
	for id, user := range s.users {
		localUsers[id] = user
	}
	resolveBotInfo := s.resolveBotInfo
	s.mu.RUnlock()

	users := make([]im.User, 0, len(apps)+len(localUsers))
	seenIDs := make(map[string]struct{}, len(apps)+len(localUsers))
	configuredBotIDs := make(map[string]struct{}, len(apps))
	for botID, rawApp := range apps {
		configuredBotIDs[botID] = struct{}{}

		app, err := validateFeishuAppConfig(rawApp, botID)
		if err != nil {
			continue
		}
		botInfo, err := resolveBotInfo(context.Background(), app)
		if err != nil {
			continue
		}
		openID := strings.TrimSpace(botInfo.OpenID)
		if openID == "" {
			continue
		}
		if _, ok := seenIDs[openID]; ok {
			continue
		}

		user, ok := localUsers[botID]
		if !ok {
			user = im.User{
				Name:      botID,
				Handle:    deriveHandle(botID, openID),
				Role:      "member",
				Avatar:    initials(botID),
				IsOnline:  true,
				CreatedAt: time.Now().UTC(),
			}
		}
		user.ID = openID
		user.AccentHex = accentHexForID(openID)
		users = append(users, user)
		seenIDs[openID] = struct{}{}
	}
	for id, user := range localUsers {
		if _, ok := configuredBotIDs[id]; ok {
			continue
		}
		if _, ok := seenIDs[user.ID]; ok {
			continue
		}
		users = append(users, user)
	}
	slices.SortFunc(users, func(a, b im.User) int { return strings.Compare(a.Name, b.Name) })
	return users
}

func (s *FeishuService) DeleteUser(userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return fmt.Errorf("user_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[userID]
	if !ok {
		return fmt.Errorf("user not found")
	}

	delete(s.users, userID)
	delete(s.byHandle, strings.ToLower(user.Handle))

	for id, room := range s.rooms {
		members := make([]string, 0, len(room.Members))
		for _, memberID := range room.Members {
			if memberID != userID {
				members = append(members, memberID)
			}
		}

		messages := make([]im.Message, 0, len(room.Messages))
		for _, message := range room.Messages {
			if message.SenderID != userID {
				messages = append(messages, message)
			}
		}

		if len(members) < 2 {
			delete(s.rooms, id)
			continue
		}

		room.Members = members
		room.Messages = messages
		room.Subtitle = formatMembers(len(members))
	}

	return nil
}

func (s *FeishuService) ResolveBotUser(ctx context.Context, botID string) (im.User, bool, error) {
	if s == nil {
		return im.User{}, false, nil
	}
	openID, _, err := s.ResolveBotOpenID(ctx, botID)
	if err != nil {
		return im.User{}, false, err
	}
	openID = strings.TrimSpace(openID)
	if openID == "" || openID == strings.TrimSpace(botID) {
		return im.User{}, false, nil
	}
	if user, ok := findFeishuUserByID(s.ListUsers(), openID); ok {
		return user, true, nil
	}
	return im.User{
		ID:        openID,
		Name:      strings.TrimSpace(botID),
		Handle:    deriveHandle(botID, openID),
		Role:      "member",
		Avatar:    initials(botID),
		IsOnline:  true,
		AccentHex: accentHexForID(openID),
		CreatedAt: time.Now().UTC(),
	}, true, nil
}

func (s *FeishuService) EnsureUser(req FeishuCreateUserRequest) (im.User, error) {
	if user, ok, err := s.ResolveBotUser(context.Background(), req.ID); err == nil && ok {
		return user, nil
	}
	if user, ok := findFeishuUserByID(s.ListUsers(), req.ID); ok {
		return user, nil
	}
	return s.CreateUser(req)
}

func findFeishuUserByID(users []im.User, id string) (im.User, bool) {
	id = strings.TrimSpace(id)
	for _, user := range users {
		if user.ID == id {
			return user, true
		}
	}
	return im.User{}, false
}

func (s *FeishuService) CreateRoom(req im.CreateRoomRequest) (im.Room, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return im.Room{}, fmt.Errorf("title is required")
	}
	creatorID := strings.TrimSpace(req.CreatorID)
	if creatorID == "" {
		return im.Room{}, fmt.Errorf("creator_id is required")
	}

	app, err := s.appConfigForCreator(creatorID)
	if err != nil {
		return im.Room{}, err
	}
	adminOpenID := strings.TrimSpace(app.AdminOpenID)
	if adminOpenID == "" {
		return im.Room{}, fmt.Errorf("feishu admin_open_id is required")
	}
	members := normalizeFeishuMembers(creatorID, req.MemberIDs)
	memberIDs := members[1:]
	description := strings.TrimSpace(req.Description)

	created, err := s.createChat(context.Background(), app, FeishuCreateChatRequest{
		Title:       title,
		Description: description,
		CreatorID:   adminOpenID, // TODO: use u-manager app_id?
		MemberIDs:   memberIDs,
	})
	if err != nil {
		return im.Room{}, err
	}
	chatID := strings.TrimSpace(created.ChatID)
	if chatID == "" {
		return im.Room{}, fmt.Errorf("create feishu chat: empty chat_id in response")
	}
	if responseName := strings.TrimSpace(created.Name); responseName != "" {
		title = responseName
	}
	if responseDescription := strings.TrimSpace(created.Description); responseDescription != "" {
		description = responseDescription
	}

	room := im.Room{
		ID:          chatID,
		Title:       title,
		Subtitle:    formatMembers(len(members)),
		Description: description,
		Members:     members,
		Messages:    nil,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.rooms[room.ID] = &room
	return cloneRoom(room), nil
}

func defaultFeishuCreateChat(ctx context.Context, app FeishuAppConfig, req FeishuCreateChatRequest) (FeishuCreateChatResponse, error) {
	client := lark.NewClient(app.AppID, app.AppSecret)
	createReq := larkim.NewCreateChatReqBuilder().
		UserIdType("open_id"). // TODO: use app_id?
		SetBotManager(true).
		Uuid(feishuRequestUUID()).
		Body(larkim.NewCreateChatReqBodyBuilder().
			Name(req.Title).
			Description(req.Description).
			OwnerId(req.CreatorID).
			UserIdList(req.MemberIDs).
			BotIdList([]string{}).
			GroupMessageType("chat").
			ChatMode("group").
			ChatType("private").
			JoinMessageVisibility("all_members").
			LeaveMessageVisibility("all_members").
			MembershipApproval("no_approval_required").
			RestrictedModeSetting(larkim.NewRestrictedModeSettingBuilder().Build()).
			UrgentSetting("all_members").
			VideoConferenceSetting("all_members").
			EditPermission("all_members").
			HideMemberCountSetting("all_members").
			Build()).
		Build()

	resp, err := client.Im.V1.Chat.Create(ctx, createReq)
	if err != nil {
		return FeishuCreateChatResponse{}, fmt.Errorf("create feishu chat: %w", err)
	}
	if !resp.Success() {
		return FeishuCreateChatResponse{}, fmt.Errorf("create feishu chat: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
	}
	if resp.Data == nil {
		return FeishuCreateChatResponse{}, fmt.Errorf("create feishu chat: empty response data")
	}
	return FeishuCreateChatResponse{
		ChatID:      larkcore.StringValue(resp.Data.ChatId),
		Name:        larkcore.StringValue(resp.Data.Name),
		Description: larkcore.StringValue(resp.Data.Description),
	}, nil
}

func defaultFeishuAddChatMembers(ctx context.Context, app FeishuAppConfig, req FeishuAddChatMembersRequest) error {
	memberAppIDs := normalizeNonEmptyStrings(req.MemberAppIDs)
	if len(memberAppIDs) == 0 {
		return fmt.Errorf("add feishu chat members: member app_ids are required")
	}

	client := lark.NewClient(app.AppID, app.AppSecret)
	addReq := larkim.NewCreateChatMembersReqBuilder().
		ChatId(req.ChatID).
		MemberIdType("app_id").
		SucceedType(0).
		Body(larkim.NewCreateChatMembersReqBodyBuilder().
			IdList(memberAppIDs).
			Build()).
		Build()

	resp, err := client.Im.V1.ChatMembers.Create(ctx, addReq)
	if err != nil {
		return fmt.Errorf("add feishu chat members: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("add feishu chat members: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
	}
	return nil
}

func defaultFeishuDeleteChat(ctx context.Context, app FeishuAppConfig, chatID string) error {
	client := lark.NewClient(app.AppID, app.AppSecret)
	resp, err := client.Im.V1.Chat.Delete(ctx, larkim.NewDeleteChatReqBuilder().
		ChatId(chatID).
		Build())
	if err != nil {
		return fmt.Errorf("delete feishu chat: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("delete feishu chat: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
	}
	return nil
}

func defaultFeishuListChatMembers(ctx context.Context, app FeishuAppConfig, apps map[string]FeishuAppConfig, chatID string) ([]im.User, error) {
	client := lark.NewClient(app.AppID, app.AppSecret)
	members := make([]im.User, 0)
	memberIDs := make(map[string]struct{})
	pageToken := ""

	for {
		reqBuilder := larkim.NewGetChatMembersReqBuilder().
			ChatId(chatID).
			MemberIdType("open_id").
			PageSize(100)
		if pageToken != "" {
			reqBuilder.PageToken(pageToken)
		}

		resp, err := client.Im.V1.ChatMembers.Get(ctx, reqBuilder.Build())
		if err != nil {
			return nil, fmt.Errorf("list feishu chat members: %w", err)
		}
		if !resp.Success() {
			return nil, fmt.Errorf("list feishu chat members: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
		}
		if resp.Data == nil {
			return nil, fmt.Errorf("list feishu chat members: empty response data")
		}

		for _, item := range resp.Data.Items {
			if item == nil {
				continue
			}
			memberID := strings.TrimSpace(larkcore.StringValue(item.MemberId))
			if memberID == "" {
				continue
			}
			if _, ok := memberIDs[memberID]; ok {
				continue
			}
			name := strings.TrimSpace(larkcore.StringValue(item.Name))
			if name == "" {
				name = memberID
			}
			memberIDs[memberID] = struct{}{}
			members = append(members, im.User{
				ID:        memberID,
				Name:      name,
				Handle:    deriveHandle(name, memberID),
				Role:      "member",
				Avatar:    initials(name),
				IsOnline:  true,
				AccentHex: accentHexForID(memberID),
			})
		}

		if !larkcore.BoolValue(resp.Data.HasMore) {
			break
		}
		pageToken = strings.TrimSpace(larkcore.StringValue(resp.Data.PageToken))
		if pageToken == "" {
			break
		}
	}

	botMembers, err := feishuBotMembersInChat(ctx, apps, chatID, memberIDs)
	if err != nil {
		return nil, err
	}
	members = append(members, botMembers...)

	return members, nil
}

func feishuBotMembersInChat(ctx context.Context, apps map[string]FeishuAppConfig, chatID string, existingMemberIDs map[string]struct{}) ([]im.User, error) {
	return feishuBotMembersInChatWithResolvers(ctx, apps, chatID, existingMemberIDs, fetchBotInfo, feishuAppIsInChat)
}

func feishuBotMembersInChatWithResolvers(
	ctx context.Context,
	apps map[string]FeishuAppConfig,
	chatID string,
	existingMemberIDs map[string]struct{},
	resolveBotInfo func(context.Context, FeishuAppConfig) (FeishuBotInfo, error),
	isInChat func(context.Context, FeishuAppConfig, string) (bool, error),
) ([]im.User, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if existingMemberIDs == nil {
		existingMemberIDs = make(map[string]struct{})
	}

	members := make([]im.User, 0, len(apps))
	for botID, rawApp := range apps {
		app, err := validateFeishuAppConfig(rawApp, botID)
		if err != nil {
			return nil, err
		}

		inChat, err := isInChat(ctx, app, chatID)
		if err != nil {
			return nil, fmt.Errorf("check feishu bot %q in chat %q: %w", botID, chatID, err)
		}
		if !inChat {
			continue
		}

		botInfo, err := resolveBotInfo(ctx, app)
		if err != nil {
			return nil, fmt.Errorf("resolve feishu bot %q open_id: %w", botID, err)
		}
		openID := strings.TrimSpace(botInfo.OpenID)
		if openID == "" {
			return nil, fmt.Errorf("resolve feishu bot %q open_id: empty open_id", botID)
		}
		if _, ok := existingMemberIDs[openID]; ok {
			continue
		}
		existingMemberIDs[openID] = struct{}{}

		name := strings.TrimSpace(botID)
		if name == "" {
			name = openID
		}
		members = append(members, im.User{
			ID:        openID,
			Name:      name,
			Handle:    deriveHandle(name, openID),
			Role:      "member",
			Avatar:    initials(name),
			IsOnline:  true,
			AccentHex: accentHexForID(openID),
		})
	}
	return members, nil
}

func feishuAppIsInChat(ctx context.Context, app FeishuAppConfig, chatID string) (bool, error) {
	client := lark.NewClient(app.AppID, app.AppSecret)
	resp, err := client.Im.V1.ChatMembers.IsInChat(ctx, larkim.NewIsInChatChatMembersReqBuilder().
		ChatId(chatID).
		Build())
	if err != nil {
		return false, err
	}
	if !resp.Success() {
		return false, fmt.Errorf("code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
	}
	if resp.Data == nil {
		return false, fmt.Errorf("empty response data")
	}
	return larkcore.BoolValue(resp.Data.IsInChat), nil
}

func defaultFeishuListChats(ctx context.Context, app FeishuAppConfig) ([]im.Room, error) {
	client := lark.NewClient(app.AppID, app.AppSecret)
	rooms := make([]im.Room, 0)
	pageToken := ""

	for {
		reqBuilder := larkim.NewListChatReqBuilder().
			UserIdType("open_id").
			SortType("ByCreateTimeAsc").
			PageSize(100)
		if pageToken != "" {
			reqBuilder.PageToken(pageToken)
		}

		resp, err := client.Im.V1.Chat.List(ctx, reqBuilder.Build())
		if err != nil {
			return nil, fmt.Errorf("list feishu chats: %w", err)
		}
		if !resp.Success() {
			return nil, fmt.Errorf("list feishu chats: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
		}
		if resp.Data == nil {
			return nil, fmt.Errorf("list feishu chats: empty response data")
		}

		for _, item := range resp.Data.Items {
			if item == nil {
				continue
			}
			chatID := strings.TrimSpace(larkcore.StringValue(item.ChatId))
			if chatID == "" {
				continue
			}
			title := strings.TrimSpace(larkcore.StringValue(item.Name))
			if title == "" {
				title = chatID
			}
			description := strings.TrimSpace(larkcore.StringValue(item.Description))
			members := normalizeNonEmptyStrings([]string{larkcore.StringValue(item.OwnerId)})
			rooms = append(rooms, im.Room{
				ID:          chatID,
				Title:       title,
				Subtitle:    formatMembers(len(members)),
				Description: description,
				Members:     members,
				Messages:    nil,
			})
		}

		if !larkcore.BoolValue(resp.Data.HasMore) {
			break
		}
		pageToken = strings.TrimSpace(larkcore.StringValue(resp.Data.PageToken))
		if pageToken == "" {
			break
		}
	}

	return rooms, nil
}

func defaultFeishuListRoomMessages(ctx context.Context, app FeishuAppConfig, chatID string) ([]im.Message, error) {
	client := lark.NewClient(app.AppID, app.AppSecret)
	messages := make([]im.Message, 0)
	pageToken := ""

	for {
		reqBuilder := larkim.NewListMessageReqBuilder().
			ContainerIdType("chat").
			ContainerId(chatID).
			StartTime("0").
			EndTime(fmt.Sprint(time.Now().UTC().Unix())).
			SortType("ByCreateTimeAsc").
			PageSize(50)
		if pageToken != "" {
			reqBuilder.PageToken(pageToken)
		}

		resp, err := client.Im.V1.Message.List(ctx, reqBuilder.Build())
		if err != nil {
			return nil, fmt.Errorf("list feishu messages: %w", err)
		}
		if !resp.Success() {
			return nil, fmt.Errorf("list feishu messages: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
		}
		if resp.Data == nil {
			return nil, fmt.Errorf("list feishu messages: empty response data")
		}

		for _, item := range resp.Data.Items {
			message, ok := feishuSDKMessageToIMMessage(item)
			if ok {
				messages = append(messages, message)
			}
		}

		if !larkcore.BoolValue(resp.Data.HasMore) {
			break
		}
		pageToken = strings.TrimSpace(larkcore.StringValue(resp.Data.PageToken))
		if pageToken == "" {
			break
		}
	}

	return messages, nil
}

func feishuSDKMessageToIMMessage(item *larkim.Message) (im.Message, bool) {
	if item == nil || larkcore.BoolValue(item.Deleted) {
		return im.Message{}, false
	}

	messageID := strings.TrimSpace(larkcore.StringValue(item.MessageId))
	if messageID == "" {
		return im.Message{}, false
	}
	senderID := ""
	if item.Sender != nil {
		senderID = strings.TrimSpace(larkcore.StringValue(item.Sender.Id))
	}
	content := ""
	if item.Body != nil {
		content = feishuMessageContentText(larkcore.StringValue(item.Body.Content))
	}

	return im.Message{
		ID:        messageID,
		SenderID:  senderID,
		Kind:      im.MessageKindMessage,
		Content:   content,
		CreatedAt: feishuMessageCreatedAt(larkcore.StringValue(item.CreateTime)),
		Mentions:  feishuMessageMentions(item.Mentions),
	}, true
}

func feishuMessageContentText(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}

	var textContent struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &textContent); err == nil && textContent.Text != "" {
		return textContent.Text
	}
	return content
}

func feishuMessageCreatedAt(createTime string) time.Time {
	createTime = strings.TrimSpace(createTime)
	if createTime == "" {
		return time.Time{}
	}
	timestamp, err := time.ParseDuration(createTime + "ms")
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(timestamp.Milliseconds()).UTC()
}

func feishuMessageMentions(mentions []*larkim.Mention) []im.Mention {
	result := make([]im.Mention, 0, len(mentions))
	for _, mention := range mentions {
		if mention == nil {
			continue
		}
		id := larkcore.StringValue(mention.Id)
		if strings.TrimSpace(id) == "" {
			continue
		}
		result = append(result, im.Mention{
			ID:   id,
			Name: larkcore.StringValue(mention.Name),
		})
	}
	return result
}

func defaultFeishuSendMessage(ctx context.Context, app FeishuAppConfig, req FeishuSendMessageRequest) (FeishuSendMessageResponse, error) {
	text := req.Content
	senderInfo, err := fetchBotInfo(ctx, app)
	if err != nil {
		return FeishuSendMessageResponse{}, err
	}
	senderOpenID := senderInfo.OpenID
	mentionID := strings.TrimSpace(req.MentionID)
	mentionOpenID := ""
	if mentionID != "" {
		mentionApp, err := validateFeishuAppConfig(req.MentionAppConfig, mentionID)
		if err != nil {
			return FeishuSendMessageResponse{}, err
		}
		botInfo, err := fetchBotInfo(ctx, mentionApp)
		if err != nil {
			return FeishuSendMessageResponse{}, err
		}
		mentionOpenID = botInfo.OpenID
		text = fmt.Sprintf("<at user_id=\"%s\">%s</at> %s", mentionOpenID, mentionID, req.Content)
	}

	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return FeishuSendMessageResponse{}, fmt.Errorf("encode feishu message content: %w", err)
	}

	client := lark.NewClient(app.AppID, app.AppSecret)
	sendReq := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(req.ChatID).
			MsgType("text").
			Content(string(content)).
			Uuid(req.UUID).
			Build()).
		Build()

	resp, err := client.Im.V1.Message.Create(ctx, sendReq)
	if err != nil {
		return FeishuSendMessageResponse{}, fmt.Errorf("send feishu message: %w", err)
	}
	if !resp.Success() {
		return FeishuSendMessageResponse{}, fmt.Errorf("send feishu message: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
	}
	if resp.Data == nil {
		return FeishuSendMessageResponse{}, fmt.Errorf("send feishu message: empty response data")
	}
	return FeishuSendMessageResponse{
		MessageID:     larkcore.StringValue(resp.Data.MessageId),
		SenderOpenID:  senderOpenID,
		MentionOpenID: mentionOpenID,
	}, nil
}

// fetchBotInfo calls the Feishu bot info API to retrieve bot identity fields.
func fetchBotInfo(ctx context.Context, app FeishuAppConfig) (FeishuBotInfo, error) {
	client := lark.NewClient(app.AppID, app.AppSecret)
	resp, err := client.Do(ctx, &larkcore.ApiReq{
		HttpMethod:                http.MethodGet,
		ApiPath:                   "/open-apis/bot/v3/info",
		SupportedAccessTokenTypes: []larkcore.AccessTokenType{larkcore.AccessTokenTypeTenant},
	})
	if err != nil {
		return FeishuBotInfo{}, fmt.Errorf("bot info request: %w", err)
	}

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Bot  struct {
			OpenID  string `json:"open_id"`
			AppName string `json:"app_name"`
		} `json:"bot"`
	}
	if err := json.Unmarshal(resp.RawBody, &result); err != nil {
		return FeishuBotInfo{}, fmt.Errorf("bot info parse: %w", err)
	}
	if result.Code != 0 {
		return FeishuBotInfo{}, fmt.Errorf("bot info api error: code=%d msg=%s", result.Code, result.Msg)
	}
	if result.Bot.OpenID == "" {
		return FeishuBotInfo{}, fmt.Errorf("bot info: empty open_id")
	}
	return FeishuBotInfo{
		OpenID:  result.Bot.OpenID,
		AppName: result.Bot.AppName,
	}, nil
}

func (s *FeishuService) SendMessage(req im.CreateMessageRequest) (im.Message, error) {
	roomID := strings.TrimSpace(req.RoomID)
	senderID := strings.TrimSpace(req.SenderID)
	content := strings.TrimSpace(req.Content)
	if roomID == "" {
		return im.Message{}, fmt.Errorf("room_id is required")
	}
	if senderID == "" {
		return im.Message{}, fmt.Errorf("sender_id is required")
	}
	if content == "" {
		return im.Message{}, fmt.Errorf("content is required")
	}

	s.mu.RLock()
	app, err := s.appConfigForSenderLocked(senderID)
	mentionID := strings.TrimSpace(req.MentionID)
	var mentionApp FeishuAppConfig
	if err == nil && mentionID != "" {
		mentionApp, err = s.appConfigForMentionLocked(mentionID)
	}
	s.mu.RUnlock()
	if err != nil {
		return im.Message{}, err
	}

	fallbackID := fmt.Sprintf("msg-%d", time.Now().UTC().UnixNano())
	sent, err := s.sendMessage(context.Background(), app, FeishuSendMessageRequest{
		ChatID:           roomID,
		Content:          content,
		UUID:             fallbackID,
		MentionID:        mentionID,
		MentionAppConfig: mentionApp,
	})
	if err != nil {
		return im.Message{}, err
	}
	senderOpenID := strings.TrimSpace(sent.SenderOpenID)
	if senderOpenID == "" {
		return im.Message{}, fmt.Errorf("resolve feishu sender open_id: empty open_id for %q", senderID)
	}
	mentionOpenID := strings.TrimSpace(sent.MentionOpenID)
	if mentionID != "" && mentionOpenID == "" {
		return im.Message{}, fmt.Errorf("resolve feishu mention open_id: empty open_id for %q", mentionID)
	}

	messageID := strings.TrimSpace(sent.MessageID)
	if messageID == "" {
		messageID = fallbackID
	}
	message := im.Message{
		ID:        messageID,
		SenderID:  senderOpenID,
		Kind:      im.MessageKindMessage,
		Content:   content,
		CreatedAt: time.Now().UTC(),
		Mentions:  nil,
	}
	if mentionOpenID != "" {
		message.Mentions = []im.Mention{{ID: mentionOpenID, Name: mentionID}}
	}

	s.mu.Lock()
	if room, ok := s.rooms[roomID]; ok {
		room.Messages = append(room.Messages, message)
	}
	s.mu.Unlock()

	if len(message.Mentions) > 0 {
		s.messageBus.Publish(FeishuMessageEvent{
			Type:    FeishuMessageEventTypeMessageCreated,
			RoomID:  roomID,
			Message: &message,
		})
	}
	return message, nil
}

func (s *FeishuService) ResolveBotOpenID(ctx context.Context, botID string) (string, string, error) {
	botID = strings.TrimSpace(botID)
	if botID == "" {
		return "", "", fmt.Errorf("feishu bot id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.RLock()
	app, ok := s.apps[botID]
	s.mu.RUnlock()
	if !ok {
		return botID, "", nil
	}

	app, err := validateFeishuAppConfig(app, botID)
	if err != nil {
		return "", "", err
	}
	botInfo, err := s.resolveBotInfo(ctx, app)
	if err != nil {
		return "", "", err
	}
	return botInfo.OpenID, botInfo.AppName, nil
}

func (s *FeishuService) ListRooms() ([]im.Room, error) {
	app, err := s.appConfigForRoom("")
	if err != nil {
		return nil, err
	}

	rooms, err := s.listChats(context.Background(), app)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range rooms {
		local, ok := s.rooms[rooms[i].ID]
		if !ok {
			continue
		}
		if len(rooms[i].Members) == 0 {
			rooms[i].Members = append([]string(nil), local.Members...)
			rooms[i].Subtitle = formatMembers(len(rooms[i].Members))
		}
		rooms[i].Messages = append([]im.Message(nil), local.Messages...)
	}
	slices.SortFunc(rooms, func(a, b im.Room) int { return strings.Compare(a.Title, b.Title) })
	return rooms, nil
}

func (s *FeishuService) ListRoomMessages(roomID string) ([]im.Message, error) {
	app, err := s.appConfigForRoom("")
	if err != nil {
		return nil, err
	}

	messages, err := s.listRoomMessages(context.Background(), app, roomID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if room, ok := s.rooms[roomID]; ok {
		room.Messages = append([]im.Message(nil), messages...)
	}
	s.mu.Unlock()

	return append([]im.Message(nil), messages...), nil
}

func (s *FeishuService) DeleteRoom(roomID string) error {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return fmt.Errorf("room_id is required")
	}

	app, err := s.appConfigForRoom(roomID)
	if err != nil {
		return err
	}
	if err := s.deleteChat(context.Background(), app, roomID); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.rooms, roomID)
	s.mu.Unlock()
	return nil
}

func (s *FeishuService) AddRoomMembers(req im.AddRoomMembersRequest) (im.Room, error) {
	roomID := strings.TrimSpace(req.RoomID)
	if roomID == "" {
		return im.Room{}, fmt.Errorf("room_id is required")
	}
	if len(req.UserIDs) == 0 {
		return im.Room{}, fmt.Errorf("user_ids is required")
	}

	s.mu.Lock()
	room, ok := s.rooms[roomID]
	existing := make(map[string]struct{})
	if ok {
		for _, userID := range room.Members {
			existing[userID] = struct{}{}
		}
	}

	newMembers := make([]string, 0, len(req.UserIDs))
	newMemberAppIDs := make([]string, 0, len(req.UserIDs))
	for _, userID := range req.UserIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		if _, ok := existing[userID]; ok {
			continue
		}
		existing[userID] = struct{}{}
		newMembers = append(newMembers, userID)
		memberAppID := userID
		if app, ok := s.apps[userID]; ok {
			if configuredAppID := strings.TrimSpace(app.AppID); configuredAppID != "" {
				memberAppID = configuredAppID
			}
		}
		newMemberAppIDs = append(newMemberAppIDs, memberAppID)
	}
	if len(newMembers) == 0 {
		s.mu.Unlock()
		return im.Room{}, fmt.Errorf("no new users to invite")
	}
	appOwnerID := strings.TrimSpace(req.InviterID)
	if appOwnerID == "" && room != nil && len(room.Members) > 0 {
		appOwnerID = room.Members[0]
	}
	app, err := s.appConfigForCreatorLocked(appOwnerID)
	if err != nil {
		s.mu.Unlock()
		return im.Room{}, err
	}
	s.mu.Unlock()

	if err := s.addChatMembers(context.Background(), app, FeishuAddChatMembersRequest{
		ChatID:       roomID,
		MemberIDs:    newMembers,
		MemberAppIDs: newMemberAppIDs,
	}); err != nil {
		return im.Room{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok = s.rooms[roomID]
	if !ok {
		return im.Room{
			ID:       roomID,
			Subtitle: formatMembers(len(newMembers)),
			Members:  append([]string(nil), newMembers...),
		}, nil
	}
	existing = make(map[string]struct{}, len(room.Members))
	for _, userID := range room.Members {
		existing[userID] = struct{}{}
	}
	for _, userID := range newMembers {
		if _, ok := existing[userID]; ok {
			continue
		}
		room.Members = append(room.Members, userID)
	}
	room.Subtitle = formatMembers(len(room.Members))
	return cloneRoom(*room), nil
}

func (s *FeishuService) ListRoomMembers(roomID string) ([]im.User, error) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return nil, fmt.Errorf("room_id is required")
	}

	app, err := s.appConfigForRoom(roomID)
	if err != nil {
		return nil, err
	}

	members, err := s.listChatMembers(context.Background(), app, s.AppConfigs(), roomID)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	users := make([]im.User, 0, len(members))
	for _, member := range members {
		if localUser, ok := s.users[member.ID]; ok {
			if member.Name != "" {
				localUser.Name = member.Name
			}
			users = append(users, localUser)
			continue
		}
		users = append(users, member)
	}
	slices.SortFunc(users, func(a, b im.User) int { return strings.Compare(a.Name, b.Name) })
	return users, nil
}

func (s *FeishuService) normalizeMembersLocked(creatorID string, memberIDs []string) ([]string, error) {
	seen := map[string]struct{}{creatorID: {}}
	members := []string{creatorID}
	for _, userID := range memberIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		if _, ok := s.users[userID]; !ok {
			return nil, fmt.Errorf("user not found: %s", userID)
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		members = append(members, userID)
	}
	return members, nil
}

func (s *FeishuService) appConfigForCreator(creatorID string) (FeishuAppConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.appConfigForCreatorLocked(creatorID)
}

func (s *FeishuService) appConfigForCreatorLocked(creatorID string) (FeishuAppConfig, error) {
	return s.managerAppConfigLocked()
}

func (s *FeishuService) appConfigForSenderLocked(senderID string) (FeishuAppConfig, error) {
	if app, ok := s.apps[senderID]; ok {
		return validateFeishuAppConfig(app, senderID)
	}
	return s.managerAppConfigLocked()
}

func (s *FeishuService) appConfigForMentionLocked(mention string) (FeishuAppConfig, error) {
	if app, ok := s.apps[mention]; ok {
		return validateFeishuAppConfig(app, mention)
	}
	return FeishuAppConfig{}, fmt.Errorf("feishu app is not configured for mention %q", mention)
}

func (s *FeishuService) managerAppConfigLocked() (FeishuAppConfig, error) {
	app, ok := s.apps[feishuManagerBotID]
	if !ok {
		return FeishuAppConfig{}, fmt.Errorf("feishu app is not configured for %q", feishuManagerBotID)
	}
	return validateFeishuAppConfig(app, feishuManagerBotID)
}

func validateFeishuAppConfig(app FeishuAppConfig, ownerID string) (FeishuAppConfig, error) {
	if strings.TrimSpace(app.AppID) == "" {
		return FeishuAppConfig{}, fmt.Errorf("feishu app_id is required for %q", ownerID)
	}
	if strings.TrimSpace(app.AppSecret) == "" {
		return FeishuAppConfig{}, fmt.Errorf("feishu app_secret is required for %q", ownerID)
	}
	return FeishuAppConfig{
		AppID:       strings.TrimSpace(app.AppID),
		AppSecret:   strings.TrimSpace(app.AppSecret),
		AdminOpenID: strings.TrimSpace(app.AdminOpenID),
	}, nil
}

func (s *FeishuService) appConfigForRoom(roomID string) (FeishuAppConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.managerAppConfigLocked()
}

func (s *FeishuService) appIDForMemberLocked(memberID string) (string, error) {
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return "", fmt.Errorf("member_id is required")
	}
	app, ok := s.apps[memberID]
	if !ok {
		return "", fmt.Errorf("feishu app is not configured for member_id %q", memberID)
	}
	appID := strings.TrimSpace(app.AppID)
	if appID == "" {
		return "", fmt.Errorf("feishu app_id is required for member_id %q", memberID)
	}
	return appID, nil
}

func normalizeNonEmptyStrings(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		normalized = append(normalized, value)
	}
	return normalized
}

func normalizeFeishuMembers(creatorID string, memberIDs []string) []string {
	seen := map[string]struct{}{creatorID: {}}
	members := []string{creatorID}
	for _, userID := range memberIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		members = append(members, userID)
	}
	return members
}

func feishuRequestUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("csgclaw-%d", time.Now().UnixNano())
	}
	return "csgclaw-" + hex.EncodeToString(b[:])
}

func normalizeFeishuUser(user im.User) im.User {
	user.ID = strings.TrimSpace(user.ID)
	user.Name = strings.TrimSpace(user.Name)
	user.Handle = strings.ToLower(strings.TrimSpace(user.Handle))
	user.Role = strings.ToLower(strings.TrimSpace(user.Role))
	if user.Handle == "" {
		user.Handle = deriveHandle(user.Name, user.ID)
	}
	if user.Role == "" {
		user.Role = "member"
	}
	if user.Avatar == "" {
		user.Avatar = initials(user.Name)
	}
	if user.AccentHex == "" {
		user.AccentHex = accentHexForID(user.ID)
	}
	return user
}

func cloneRoom(room im.Room) im.Room {
	room.Members = append([]string(nil), room.Members...)
	room.Messages = append([]im.Message(nil), room.Messages...)
	return room
}

func deriveHandle(name, fallback string) string {
	source := strings.ToLower(strings.TrimSpace(name))
	if source == "" {
		source = strings.ToLower(strings.TrimSpace(fallback))
	}
	var b strings.Builder
	for _, r := range source {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	handle := strings.Trim(b.String(), "-._")
	if handle == "" {
		return strings.ToLower(strings.TrimSpace(fallback))
	}
	return handle
}

func initials(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "FS"
	}
	parts := strings.Fields(name)
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		r := []rune(part)[0]
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		b.WriteRune(r)
		if b.Len() >= 2 {
			break
		}
	}
	if b.Len() == 0 {
		return "FS"
	}
	return b.String()
}

func accentHexForID(id string) string {
	palette := []string{"#0f766e", "#2563eb", "#7c3aed", "#dc2626", "#ca8a04", "#16a34a"}
	sum := 0
	for _, r := range id {
		sum += int(r)
	}
	return palette[sum%len(palette)]
}

func formatMembers(n int) string {
	if n == 1 {
		return "1 member"
	}
	return fmt.Sprintf("%d members", n)
}

func (s *FeishuService) SetConfigPath(path string) {
	if s == nil {
		return
	}
	s.configStore.SetPath(path)
}

func (s *FeishuService) SetConfigReloadHook(hook FeishuConfigReloadHook) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configReloadHook = hook
}

func (s *FeishuService) GetConfig(botID string) (feishuconfig.Entry, error) {
	return s.configStore.Get(botID)
}

func (s *FeishuService) UpdateConfig(update feishuconfig.Update) (feishuconfig.Entry, error) {
	return s.configStore.Update(update)
}

func (s *FeishuService) ReloadConfig() ([]string, error) {
	cfg, err := s.configStore.Load()
	if err != nil {
		return nil, err
	}
	s.SetAppConfigs(convertFeishuConfigApps(feishuconfig.AppsFromChannels(cfg.Channels)))
	if hook := s.configReloadHookSnapshot(); hook != nil {
		hook(cfg.Channels)
	}
	return feishuconfig.SortedBotIDs(cfg.Channels), nil
}

func (s *FeishuService) configReloadHookSnapshot() FeishuConfigReloadHook {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configReloadHook
}

func convertFeishuConfigApps(apps map[string]feishuconfig.AppConfig) map[string]FeishuAppConfig {
	converted := make(map[string]FeishuAppConfig, len(apps))
	for name, app := range apps {
		converted[name] = FeishuAppConfig{
			AppID:       app.AppID,
			AppSecret:   app.AppSecret,
			AdminOpenID: app.AdminOpenID,
		}
	}
	return converted
}
