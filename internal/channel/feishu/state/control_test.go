package state

import (
	channel "csgclaw/internal/channel"
	"testing"
)

func TestCancelingPreservesTerminalStateAndPresentation(t *testing.T) {
	for _, status := range []channel.TurnStatus{channel.TurnRunning, channel.TurnSucceeded, channel.TurnCanceled, channel.TurnFailed} {
		t.Run(string(status), func(t *testing.T) {
			store := NewStore()
			if err := store.Put(channel.TurnRecord{TurnID: "turn", BindingID: "binding", AgentID: "agent", ConversationKey: "conversation", Status: status}); err != nil {
				t.Fatal(err)
			}
			update := channel.DeliveryIntent{ID: "canceling", BindingID: "binding", TurnID: "turn", Kind: channel.DeliveryCardUpdate}
			if err := store.MarkCanceling("turn", update); err != nil {
				t.Fatal(err)
			}
			record, _ := store.Get("turn")
			_, queued := store.Delivery(update.ID)
			if status == channel.TurnRunning {
				if record.Status != channel.TurnCanceling || !queued {
					t.Fatal("missing cancellation state or update")
				}
			} else if record.Status != status || queued {
				t.Fatal("cancellation overwrote terminal state or queued stale presentation")
			}
		})
	}
}
