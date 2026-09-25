package presentation

import (
	channel "csgclaw/internal/channel"
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskControlUsesCallbackWithoutChatCommand(t *testing.T) {
	for _, card := range []map[string]any{TaskControl(channel.TurnRunning), COTCompletionFailureCard()} {
		raw, _ := json.Marshal(card)
		text := string(raw)
		if !strings.Contains(text, `"type":"callback"`) || !strings.Contains(text, `"operation":"cancel"`) || strings.Contains(text, "/stop") {
			t.Fatalf("control=%s", text)
		}
	}
	for _, status := range []channel.TurnStatus{channel.TurnCanceling, channel.TurnCanceled, channel.TurnSucceeded, channel.TurnFailed} {
		raw, _ := json.Marshal(TaskControl(status))
		if strings.Contains(string(raw), `"tag":"button"`) {
			t.Fatalf("status %s still has a stop button", status)
		}
	}
}
