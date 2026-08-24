package ingress

import (
	"context"
	"testing"

	channeltypes "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/transport"
)

type quotedMessageAdapter struct {
	requested string
	message   transport.Message
}

func (a *quotedMessageAdapter) FetchMessage(_ context.Context, messageID string) (transport.Message, bool, error) {
	a.requested = messageID
	return a.message, true, nil
}

func TestHydrateQuotedMessagePrefersParentAndKeepsStructuredContext(t *testing.T) {
	adapter := &quotedMessageAdapter{message: transport.Message{
		ID: "om-parent", Sender: transport.Identity{OpenID: "ou-manager", Name: "manager"},
		SenderType: transport.SenderBot, Text: "@u-dev 请自我介绍一下",
	}}
	message := channeltypes.InboundMessage{Source: channeltypes.Source{
		MessageID: "om-current", ParentID: "om-parent", RootID: "om-root",
	}}
	got, err := hydrateQuotedMessage(context.Background(), adapter, message)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.requested != "om-parent" || got.QuotedMessage == nil ||
		got.QuotedMessage.ID != "om-parent" || got.QuotedMessage.SenderID != "ou-manager" ||
		got.QuotedMessage.Text != "@u-dev 请自我介绍一下" {
		t.Fatalf("hydrated message = %#v, requested %q", got.QuotedMessage, adapter.requested)
	}
}
