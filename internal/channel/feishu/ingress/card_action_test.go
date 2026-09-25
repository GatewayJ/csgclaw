package ingress

import (
	"context"
	"errors"
	"testing"

	channeltypes "csgclaw/internal/channel"
	feishuctx "csgclaw/internal/channel/feishu/context"
	"csgclaw/internal/channel/feishu/interaction"
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

func TestResolutionRouteUsesDeliveredIdentityAndRejectsOtherUser(t *testing.T) {
	store := feishustate.NewStore()
	binding := channeltypes.Binding{ID: "binding", AgentID: "agent", Channel: "feishu"}
	_ = store.Put(channeltypes.TurnRecord{TurnID: "turn", BindingID: "binding", AgentID: "agent", ConversationKey: "conversation", Status: channeltypes.TurnSucceeded})
	card := channeltypes.DeliveryIntent{ID: "question", TurnID: "turn", BindingID: "binding", RequesterID: "human", ChatID: "chat", Kind: channeltypes.DeliveryCard, InteractionID: "trusted-interaction"}
	_ = store.Enqueue(card)
	card.MessageID = "remote-card"
	_ = store.MarkDelivered(card)
	action := &transport.CardAction{MessageID: "remote-card", ChatID: "chat", Operator: transport.Identity{OpenID: "other"}, ActionValue: map[string]any{"operation": "resolve", "interaction_id": "forged", "agent_id": "forged"}}
	event := transport.Event{Kind: transport.EventCardAction, EventID: "callback", CardAction: action}
	got, err := normalizeCardAction(binding, event, fixedActiveTurn(""), store)
	if err != nil || got.trusted {
		t.Fatalf("other user's route=%+v err=%v", got, err)
	}
	action.Operator.OpenID = "human"
	got, err = normalizeCardAction(binding, event, fixedActiveTurn(""), store)
	if err != nil || !got.trusted || got.input.InteractionID != "trusted-interaction" || got.input.AgentID != "agent" || got.input.TurnID != "turn" {
		t.Fatalf("trusted route=%+v err=%v", got, err)
	}
}

func TestCOTStopRoutesOnlyToTrustedProcessMessage(t *testing.T) {
	store := feishustate.NewStore()
	binding := channeltypes.Binding{ID: "binding", AgentID: "agent", Channel: "feishu"}
	_ = store.Put(channeltypes.TurnRecord{TurnID: "old", BindingID: binding.ID, AgentID: binding.AgentID, ConversationKey: "conversation", Status: channeltypes.TurnSucceeded})
	create := channeltypes.DeliveryIntent{ID: "old:cot:create", TurnID: "old", BindingID: binding.ID, Kind: channeltypes.DeliveryCOTCreate, ChatID: "chat", RequesterID: "owner", COTID: "cot", MessageID: "process"}
	_ = store.Enqueue(create)
	_ = store.MarkDelivered(create)
	for _, tc := range []struct {
		name, user, message, cmd string
		trusted                  bool
	}{
		{"owner", "owner", "process", "stop", true},
		{"other user", "other", "process", "stop", false},
		{"unknown message", "owner", "unknown", "stop", false},
		{"unsupported operation", "owner", "process", "reset", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := transport.Event{EventID: "click", CardAction: &transport.CardAction{MessageID: tc.message, ChatID: "chat", Operator: transport.Identity{OpenID: tc.user}, ActionValue: map[string]any{"cmd": tc.cmd, "turn_id": "new"}}}
			got, err := normalizeCardAction(binding, event, fixedActiveTurn("new"), store)
			if err != nil {
				t.Fatal(err)
			}
			if got.trusted != tc.trusted {
				t.Fatalf("route=%+v", got)
			}
			if tc.trusted && (got.cotCreateID != create.ID || got.input.TurnID != "old") {
				t.Fatalf("wrong COT route=%+v", got)
			}
		})
	}
}

type cotCancelRunner struct {
	testIntakeRunner
	active   string
	canceled string
}

func (r *cotCancelRunner) ActiveTurn(string) string { return r.active }
func (r *cotCancelRunner) Cancel(_ context.Context, _, _, turn string) error {
	r.canceled = turn
	return nil
}

func TestCOTStopCompletesOldProcessWithoutCancelingNewTurn(t *testing.T) {
	for _, active := range []string{"old", "new", ""} {
		t.Run("active="+active, func(t *testing.T) {
			store := feishustate.NewStore()
			create := channeltypes.DeliveryIntent{ID: "old:cot:create", TurnID: "old", BindingID: "binding", Kind: channeltypes.DeliveryCOTCreate, COTID: "cot", MessageID: "process"}
			_ = store.Enqueue(create)
			_ = store.MarkDelivered(create)
			complete := channeltypes.DeliveryIntent{ID: "old:cot:complete", TurnID: "old", BindingID: "binding", Kind: channeltypes.DeliveryCOTComplete, RelatedID: create.ID}
			_ = store.Enqueue(complete)
			_ = store.MarkFailed(complete.ID, errors.New("unavailable"))
			runner := &cotCancelRunner{active: active}
			intake := &Intake{state: store, runner: runner}
			card := normalizedCardAction{cotCreateID: create.ID, input: interaction.Input{AgentID: "agent", ConversationKey: "conversation", TurnID: "old"}}
			if err := intake.handleCard(context.Background(), card); err != nil {
				t.Fatal(err)
			}
			if (runner.canceled == "old") != (active == "old") {
				t.Fatalf("active=%s canceled=%s", active, runner.canceled)
			}
			got, _ := store.Delivery(complete.ID)
			if got.Status != channeltypes.DeliveryPending {
				t.Fatalf("completion=%+v", got)
			}
		})
	}
}
