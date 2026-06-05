package api

import (
	"context"
	"log/slog"
	"strings"

	"csgclaw/internal/agent"
	csgclawchannel "csgclaw/internal/channel/csgclaw"
	"csgclaw/internal/im"
	"csgclaw/internal/slashcommand"
)

const feishuChannelID = "feishu"

func newConversationCommandReason(content string) (string, bool, error) {
	cmd, ok, err := slashcommand.Parse(content)
	if err != nil || !ok {
		return "", ok, err
	}
	if !slashcommand.IsNewConversationCommand(cmd) {
		return "", false, nil
	}
	return strings.TrimSpace(cmd.Body), true, nil
}

func (h *Handler) publishNewConversationBotEvent(ctx context.Context, room im.Room, sender im.User, message im.Message, reason string) []string {
	if h == nil || h.svc == nil || h.botBridge == nil {
		return nil
	}
	var missed []string
	threadRootID := conversationThreadRootID(message)
	for _, botID := range newConversationTargets(room, message, h.isAgentSender) {
		action, err := h.svc.NewConversationAction(ctx, agent.NewConversationRequest{
			Channel:      csgclawchannel.ChannelID,
			BotID:        botID,
			RoomID:       room.ID,
			ThreadRootID: threadRootID,
			Reason:       reason,
		})
		if err != nil {
			slog.Warn("new conversation action failed", "channel", csgclawchannel.ChannelID, "bot_id", botID, "room_id", room.ID, "error", err)
			continue
		}
		switch action.Mode {
		case agent.NewConversationActionBotEvent:
			if action.BotEventText == "" {
				continue
			}
			if !h.botBridge.EnqueueMessageEventWithText(room, sender, message, botID, action.BotEventText) {
				missed = append(missed, botID)
			}
		case agent.NewConversationActionInternal:
			if !h.botBridge.EnqueueMessageEvent(room, sender, message, botID) {
				missed = append(missed, botID)
			}
		}
	}
	return missed
}

func (h *Handler) rewriteFeishuNewConversationEvent(ctx context.Context, botID string, evt im.Message, roomID string, reason string) (im.Message, bool) {
	if h == nil || h.svc == nil {
		return evt, false
	}
	action, err := h.svc.NewConversationAction(ctx, agent.NewConversationRequest{
		Channel: feishuChannelID,
		BotID:   botID,
		RoomID:  roomID,
		Reason:  reason,
	})
	if err != nil {
		slog.Warn("new conversation action failed", "channel", feishuChannelID, "bot_id", botID, "room_id", roomID, "error", err)
		return evt, false
	}
	switch action.Mode {
	case agent.NewConversationActionBotEvent:
		if action.BotEventText == "" {
			return evt, false
		}
		evt.Content = action.BotEventText
		return evt, true
	case agent.NewConversationActionInternal:
		slog.Warn("new conversation internal action is not supported on feishu channel", "channel", feishuChannelID, "bot_id", botID, "room_id", roomID)
		return evt, false
	default:
		return evt, false
	}
}

func newConversationTargets(room im.Room, message im.Message, isAgent func(string) bool) []string {
	if isAgent == nil {
		return nil
	}
	targets := make([]string, 0)
	for _, memberID := range room.Members {
		memberID = strings.TrimSpace(memberID)
		if memberID == "" || memberID == strings.TrimSpace(message.SenderID) || !isAgent(memberID) {
			continue
		}
		if !room.IsDirect && !messageMentions(message, memberID) {
			continue
		}
		targets = append(targets, memberID)
	}
	return targets
}

func messageMentions(message im.Message, userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false
	}
	for _, mention := range message.Mentions {
		if strings.TrimSpace(mention.ID) == userID {
			return true
		}
	}
	return false
}

func conversationThreadRootID(message im.Message) string {
	if message.RelatesTo == nil || strings.TrimSpace(message.RelatesTo.RelType) != im.RelationTypeThread {
		return ""
	}
	return strings.TrimSpace(message.RelatesTo.EventID)
}
