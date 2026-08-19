package ingress

import (
	"context"
	"fmt"
	"strings"

	"csgclaw/internal/agentengine"
	channeltypes "csgclaw/internal/channel"
	feishuctx "csgclaw/internal/channel/feishu/context"
	"csgclaw/internal/channel/feishu/interaction"
	"csgclaw/internal/channel/feishu/transport"
)

type normalizedCardAction struct {
	source          channeltypes.Source
	turnID          string
	conversationKey string
	input           interaction.Input
	successText     string
	trusted         bool
}

const expiredCardActionText = "This card has expired and was not applied."

type activeTurnLookup interface {
	ActiveTurn(string) string
}

type cardRouteState interface {
	DeliveryByRemoteMessage(string, channeltypes.DeliveryKind, string) (channeltypes.DeliveryIntent, bool, error)
	Get(string) (channeltypes.TurnRecord, bool)
}

func normalizeCardAction(binding channeltypes.Binding, event transport.Event, runner activeTurnLookup, state cardRouteState) (normalizedCardAction, error) {
	action := event.CardAction
	if action == nil {
		return normalizedCardAction{}, fmt.Errorf("Feishu card action payload is required")
	}
	eventID := strings.TrimSpace(event.EventID)
	if eventID == "" {
		return normalizedCardAction{}, fmt.Errorf("Feishu card action event ID is required")
	}
	chatID := strings.TrimSpace(action.ChatID)
	if chatID == "" {
		return normalizedCardAction{}, fmt.Errorf("Feishu card action chat ID is required")
	}
	threadID := strings.TrimSpace(action.ThreadID)
	operation := interaction.Operation(strings.ToLower(firstMapString(action.ActionValue, "operation", "action", "csgclaw_action")))
	if operation == "" && strings.EqualFold(firstMapString(action.ActionValue, "cmd"), "stop") {
		// The local CardKit renderer emits cmd=stop. The callback remains
		// bound to the trusted remote card message ID before it can cancel a Turn.
		operation = interaction.OperationCancel
	}
	successText := map[interaction.Operation]string{
		interaction.OperationCancel: "Canceled the active turn.",
		interaction.OperationReset:  "Cleared my internal history for this conversation. The IM room messages were not cleared.",
	}[operation]
	route, routed, err := trustedCardRoute(binding, action, state)
	if err != nil {
		return normalizedCardAction{}, err
	}
	card := normalizedCardAction{
		source: channeltypes.Source{
			Channel:   binding.Channel,
			BindingID: binding.ID,
			EventID:   eventID,
			MessageID: strings.TrimSpace(action.MessageID),
			ChatID:    chatID,
			ChatType:  strings.TrimSpace(string(action.ChatType)),
			ThreadID:  threadID,
		},
		turnID:      feishuctx.TurnID(binding.ID, eventID, action.MessageID),
		successText: expiredCardActionText,
	}
	if !routed {
		// Card action values are untrusted. Without a locally delivered card
		// record, no action is allowed to select an Agent, Conversation, or Turn.
		return card, nil
	}
	// The public Ingress callback intentionally does not manufacture a thread
	// ID. Reconstruct it only from the card delivery that CSGClaw recorded, so
	// card controls keep the same Engine conversation and reply target as the
	// originating card.
	threadID = route.intent.ThreadID
	card.source.ThreadID = threadID
	conversationKey := route.record.ConversationKey
	turnID := ""
	if operation == interaction.OperationCancel {
		turnID = route.record.TurnID
		if runner == nil || runner.ActiveTurn(route.record.ConversationKey) != route.record.TurnID {
			successText = "The requested turn is no longer active."
		}
	}
	card.conversationKey = conversationKey
	card.input = interaction.Input{
		AgentID:         route.record.AgentID,
		ConversationKey: conversationKey,
		TurnID:          turnID,
		Action:          interaction.CardAction{Operation: operation},
	}
	card.successText = successText
	card.trusted = true
	return card, nil
}

type trustedCardRouteResult struct {
	intent channeltypes.DeliveryIntent
	record channeltypes.TurnRecord
}

func trustedCardRoute(binding channeltypes.Binding, action *transport.CardAction, state cardRouteState) (trustedCardRouteResult, bool, error) {
	if state == nil || action == nil || strings.TrimSpace(action.MessageID) == "" {
		return trustedCardRouteResult{}, false, nil
	}
	intent, found, err := state.DeliveryByRemoteMessage(binding.ID, channeltypes.DeliveryCard, action.MessageID)
	if err != nil {
		return trustedCardRouteResult{}, false, fmt.Errorf("resolve trusted Feishu card route: %w", err)
	}
	if !found || intent.BindingID != binding.ID || intent.ChatID != strings.TrimSpace(action.ChatID) {
		return trustedCardRouteResult{}, false, nil
	}
	// Older transports provided a carrier thread ID. If it is present, it is an
	// additional consistency check; generic Ingress intentionally leaves it
	// empty and relies on the trusted delivery record above.
	if threadID := strings.TrimSpace(action.ThreadID); threadID != "" && strings.TrimSpace(intent.ThreadID) != threadID {
		return trustedCardRouteResult{}, false, nil
	}
	record, found := state.Get(intent.TurnID)
	if !found || record.TurnID != intent.TurnID || record.BindingID != binding.ID ||
		record.AgentID != binding.AgentID || strings.TrimSpace(record.ConversationKey) == "" {
		return trustedCardRouteResult{}, false, nil
	}
	return trustedCardRouteResult{intent: intent, record: record}, true, nil
}

func firstMapString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" {
				return text
			}
		}
	}
	return ""
}

func cardActionErrorText(err error) string {
	switch agentengine.ErrorCodeOf(err) {
	case agentengine.ErrorConversationBusy:
		return "This conversation is busy; the action was not applied."
	case agentengine.ErrorInteractionNotFound:
		return "This interaction is no longer active."
	case agentengine.ErrorAgentUnavailable:
		return "Agent is currently unavailable; the action was not applied."
	default:
		return "The card action could not be applied."
	}
}

type activeTurnCanceler interface {
	Cancel(context.Context, string, string, string) error
}

func handleCardAction(ctx context.Context, handler *interaction.Handler, canceler activeTurnCanceler, item normalizedCardAction) error {
	if item.input.Action.Operation == interaction.OperationCancel && canceler != nil {
		return canceler.Cancel(ctx, item.input.AgentID, item.input.ConversationKey, item.input.TurnID)
	}
	if handler == nil {
		return fmt.Errorf("Feishu card action handler is unavailable")
	}
	return handler.Handle(ctx, item.input)
}
