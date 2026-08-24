package ingress

import (
	"testing"

	channeltypes "csgclaw/internal/channel"
	feishuctx "csgclaw/internal/channel/feishu/context"
	feishustate "csgclaw/internal/channel/feishu/state"
	"csgclaw/internal/channel/feishu/transport"
)

type fixedActiveTurn string

func (t fixedActiveTurn) ActiveTurn(string) string { return string(t) }

func TestCancelCardUsesDeliveredCardTurnInsteadOfCurrentTurn(t *testing.T) {
	store := feishustate.NewStore()
	binding := channeltypes.Binding{ID: "feishu:participant", Channel: "feishu", AgentID: "agent-1"}
	conversationKey := feishuctx.ChatConversationKey(binding.ID, "chat-1", "thread-1")
	record := channeltypes.TurnRecord{
		TurnID: "turn-old", AgentID: binding.AgentID, BindingID: binding.ID,
		ConversationKey: conversationKey, Status: channeltypes.TurnRunning,
	}
	if err := store.Put(record); err != nil {
		t.Fatal(err)
	}
	card := channeltypes.DeliveryIntent{
		ID: "turn-old:card:create", BindingID: binding.ID, TurnID: record.TurnID,
		Kind: channeltypes.DeliveryCard, ChatID: "chat-1", ThreadID: "thread-1", Card: map[string]any{"schema": "2.0"},
	}
	if err := store.Enqueue(card); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginDelivery(card.ID); err != nil {
		t.Fatal(err)
	}
	card.MessageID = "om_old_card"
	if err := store.MarkDelivered(card); err != nil {
		t.Fatal(err)
	}

	got, err := normalizeCardAction(binding, transport.Event{
		Kind: transport.EventCardAction, EventID: "event-click",
		CardAction: &transport.CardAction{
			MessageID: "om_old_card", ChatID: "chat-1",
			ActionValue: map[string]any{"cmd": "stop"},
		},
	}, fixedActiveTurn("turn-new"), store)
	if err != nil {
		t.Fatal(err)
	}
	if !got.trusted || got.input.TurnID != "turn-old" || got.input.ConversationKey != conversationKey || got.input.AgentID != binding.AgentID {
		t.Fatalf("trusted cancel route = %#v", got.input)
	}
	if got.source.ThreadID != "thread-1" {
		t.Fatalf("card response thread = %q, want thread-1", got.source.ThreadID)
	}
	if got.successText != "The requested turn is no longer active." {
		t.Fatalf("stale cancel result = %q", got.successText)
	}

	reset, err := normalizeCardAction(binding, transport.Event{
		Kind: transport.EventCardAction, EventID: "event-reset",
		CardAction: &transport.CardAction{
			MessageID: "om_old_card", ChatID: "chat-1", ActionValue: map[string]any{"operation": "reset"},
		},
	}, fixedActiveTurn("turn-new"), store)
	if err != nil {
		t.Fatal(err)
	}
	if !reset.trusted || reset.input.ConversationKey != conversationKey || reset.input.AgentID != binding.AgentID || reset.source.ThreadID != "thread-1" {
		t.Fatalf("trusted reset route = %#v / %#v", reset.input, reset.source)
	}
}

func TestUnknownCancelCardCannotTargetCurrentTurn(t *testing.T) {
	store := feishustate.NewStore()
	binding := channeltypes.Binding{ID: "feishu:participant", Channel: "feishu", AgentID: "agent-1"}
	got, err := normalizeCardAction(binding, transport.Event{
		Kind: transport.EventCardAction, EventID: "event-click",
		CardAction: &transport.CardAction{
			MessageID: "om_unknown", ChatID: "chat-1",
			ActionValue: map[string]any{"operation": "cancel"},
		},
	}, fixedActiveTurn("turn-current"), store)
	if err != nil {
		t.Fatal(err)
	}
	if got.trusted || got.input.AgentID != "" || got.input.ConversationKey != "" || got.input.TurnID != "" {
		t.Fatalf("unknown card carried a control route: %#v", got)
	}
	if got.successText != expiredCardActionText {
		t.Fatalf("unknown card reply = %q", got.successText)
	}
}
