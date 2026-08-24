package ingress

import (
	"context"
	"strings"

	channeltypes "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/transport"
)

func hydrateQuotedMessage(ctx context.Context, messages transport.MessageAdapter, message channeltypes.InboundMessage) (channeltypes.InboundMessage, error) {
	quotedID := firstNonEmpty(message.Source.ParentID, message.Source.RootID)
	if messages == nil || quotedID == "" || quotedID == strings.TrimSpace(message.Source.MessageID) {
		return message, nil
	}
	quoted, found, err := messages.FetchMessage(ctx, quotedID)
	if err != nil || !found {
		return message, err
	}
	message.QuotedMessage = &channeltypes.QuotedMessage{
		ID:         firstNonEmpty(quoted.ID, quotedID),
		SenderID:   firstNonEmpty(quoted.Sender.OpenID, quoted.Sender.UserID, quoted.Sender.UnionID),
		SenderName: strings.TrimSpace(quoted.Sender.Name),
		SenderType: strings.TrimSpace(string(quoted.SenderType)),
		Text:       quoted.Text,
	}
	return message, nil
}
