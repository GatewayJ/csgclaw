package agent

import (
	"context"
	"fmt"
	"strings"

	agentruntime "csgclaw/internal/runtime"
)

type NewConversationActionMode string

const (
	NewConversationActionBotEvent NewConversationActionMode = "bot_event"
	NewConversationActionInternal NewConversationActionMode = "internal"
)

type NewConversationRequest struct {
	Channel      string
	BotID        string
	RoomID       string
	ThreadRootID string
	Reason       string
}

type NewConversationAction struct {
	Mode         NewConversationActionMode
	BotEventText string
	AckText      string
}

func (s *Service) NewConversationAction(ctx context.Context, req NewConversationRequest) (NewConversationAction, error) {
	if s == nil {
		return NewConversationAction{}, fmt.Errorf("agent service is required")
	}
	botID := strings.TrimSpace(req.BotID)
	if botID == "" {
		return NewConversationAction{}, fmt.Errorf("bot id is required")
	}
	got, ok := s.agentSnapshot(botID)
	if !ok {
		return NewConversationAction{}, fmt.Errorf("agent %q not found", botID)
	}
	runtimeImpl, err := s.runtimeForKind(strings.TrimSpace(got.RuntimeKind))
	if err != nil {
		return NewConversationAction{}, err
	}
	starter, ok := runtimeImpl.(agentruntime.ConversationStarter)
	if !ok {
		return NewConversationAction{}, fmt.Errorf("runtime %q does not support new conversation", runtimeImpl.Kind())
	}
	action, err := starter.NewConversation(ctx, runtimeHandleForAgent(got), agentruntime.ConversationStartRequest{
		Channel:      strings.TrimSpace(req.Channel),
		BotID:        botID,
		RoomID:       strings.TrimSpace(req.RoomID),
		ThreadRootID: strings.TrimSpace(req.ThreadRootID),
		Reason:       strings.TrimSpace(req.Reason),
	})
	if err != nil {
		return NewConversationAction{}, err
	}
	return NewConversationAction{
		Mode:         NewConversationActionMode(action.Mode),
		BotEventText: strings.TrimSpace(action.BotEventText),
		AckText:      strings.TrimSpace(action.AckText),
	}, nil
}
