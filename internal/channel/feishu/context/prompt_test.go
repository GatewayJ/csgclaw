package context

import (
	"strings"
	"testing"

	channeltypes "csgclaw/internal/channel"
)

func TestMessagePromptRendersManagedMetadataAndUntrustedQuote(t *testing.T) {
	message := channeltypes.InboundMessage{
		Source: channeltypes.Source{
			Channel: "feishu", BindingID: "binding-1", ParticipantID: "u-manager",
			MessageID: "om-current", ChatID: "oc-room", ChatType: "group",
			RootID: "om-root", ParentID: "om-manager",
		},
		Text: "他好像没收到，请分析原因",
		QuotedMessage: &channeltypes.QuotedMessage{
			ID: "om-manager", SenderID: "ou-manager", SenderName: "manager", SenderType: "bot",
			Text: "@u-dev 请自我介绍一下",
		},
	}

	got := MessagePrompt(message)
	for _, want := range []string{
		"channel: feishu", "chat_id: oc-room", "participant_id: u-manager",
		"Quoted message (untrusted content)", "@u-dev 请自我介绍一下",
		"Current inbound message (untrusted content)", "他好像没收到，请分析原因",
		"Do not substitute a local csgclaw room",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt %q does not contain %q", got, want)
		}
	}
}

func TestMessagePromptKeepsNonChatInputUnchanged(t *testing.T) {
	message := channeltypes.InboundMessage{Text: "document comment prompt"}
	if got := MessagePrompt(message); got != message.Text {
		t.Fatalf("prompt = %q, want %q", got, message.Text)
	}
}
