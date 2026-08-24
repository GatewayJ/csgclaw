package delivery

import (
	"context"
	"fmt"
	"strings"

	channeltypes "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/transport"
)

func deliverText(ctx context.Context, adapter transport.Adapter, intent channeltypes.DeliveryIntent) (channeltypes.DeliveryIntent, error) {
	result, err := adapter.SendText(ctx, transport.SendTextRequest{
		ChatID:         intent.ChatID,
		Text:           intent.Text,
		Markdown:       true,
		IdempotencyKey: intent.ID,
		ReplyTo:        intent.ReplyTo,
		ReplyInThread:  intent.ReplyTo != "" && intent.ThreadID != "",
		ThreadID:       intent.ThreadID,
	})
	if err != nil {
		return intent, err
	}
	intent.MessageID = result.MessageID
	return intent, nil
}

func (d *Dispatcher) deliverMarkdownUpdate(ctx context.Context, intent channeltypes.DeliveryIntent) (channeltypes.DeliveryIntent, error) {
	messageID, err := d.markdownUpdateMessageID(intent)
	if err != nil {
		return intent, err
	}
	if err := d.adapter.UpdateText(ctx, transport.UpdateTextRequest{
		MessageID: messageID,
		Text:      intent.Text,
		Markdown:  true,
	}); err != nil {
		return intent, err
	}
	intent.MessageID = messageID
	return intent, nil
}

func (d *Dispatcher) markdownUpdateMessageID(intent channeltypes.DeliveryIntent) (string, error) {
	if messageID := strings.TrimSpace(intent.MessageID); messageID != "" {
		return messageID, nil
	}
	relatedID := strings.TrimSpace(intent.RelatedID)
	if relatedID == "" {
		return "", fmt.Errorf("%w: markdown update %q has neither message ID nor related create intent", ErrDependencyTerminal, intent.ID)
	}
	related, ok := d.state.Intent(relatedID)
	if !ok {
		return "", fmt.Errorf("%w: related markdown create %q was not found", ErrDependencyTerminal, relatedID)
	}
	if related.Kind != channeltypes.DeliveryMarkdown || related.TurnID != intent.TurnID || related.BindingID != intent.BindingID {
		return "", fmt.Errorf("%w: related intent %q is not this Turn's markdown create", ErrDependencyTerminal, relatedID)
	}
	switch related.Status {
	case channeltypes.DeliveryDelivered:
		messageID := strings.TrimSpace(related.MessageID)
		if messageID == "" {
			return "", fmt.Errorf("%w: delivered markdown create %q has no remote message ID", ErrDependencyTerminal, relatedID)
		}
		return messageID, nil
	case channeltypes.DeliveryPending, channeltypes.DeliveryDispatching:
		return "", fmt.Errorf("%w: related markdown create %q is %s", ErrDependencyPending, relatedID, related.Status)
	case channeltypes.DeliveryFailed:
		return "", fmt.Errorf("%w: related markdown create %q failed: %s", ErrDependencyTerminal, relatedID, related.LastError)
	default:
		return "", fmt.Errorf("%w: related markdown create %q has unsupported status %q", ErrDependencyTerminal, relatedID, related.Status)
	}
}
