package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	channeltypes "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/transport"
)

var (
	// ErrDependencyPending means a delivery must wait for its related create
	// intent to produce a remote identifier.
	ErrDependencyPending = errors.New("feishu delivery dependency pending")
	// ErrDependencyTerminal means the related create can never provide the
	// remote identifier required by this delivery.
	ErrDependencyTerminal = errors.New("feishu delivery dependency terminal")
)

func deliverCard(ctx context.Context, adapter transport.Adapter, intent channeltypes.DeliveryIntent) (channeltypes.DeliveryIntent, error) {
	result, err := adapter.SendCard(ctx, transport.SendCardRequest{
		ChatID:         intent.ChatID,
		Card:           intent.Card,
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

func (d *Dispatcher) deliverCardUpdate(ctx context.Context, intent channeltypes.DeliveryIntent) (channeltypes.DeliveryIntent, error) {
	messageID, err := d.cardUpdateMessageID(intent)
	if err != nil {
		return intent, err
	}
	if err := d.adapter.UpdateCard(ctx, transport.UpdateCardRequest{MessageID: messageID, Card: intent.Card}); err != nil {
		return intent, err
	}
	intent.MessageID = messageID
	return intent, nil
}

func (d *Dispatcher) cardUpdateMessageID(intent channeltypes.DeliveryIntent) (string, error) {
	if messageID := strings.TrimSpace(intent.MessageID); messageID != "" {
		return messageID, nil
	}
	relatedID := strings.TrimSpace(intent.RelatedID)
	if relatedID == "" {
		return "", fmt.Errorf("%w: card update %q has neither message ID nor related create intent", ErrDependencyTerminal, intent.ID)
	}
	related, ok := d.state.Intent(relatedID)
	if !ok {
		return "", fmt.Errorf("%w: related card create %q was not found", ErrDependencyTerminal, relatedID)
	}
	if related.Kind != channeltypes.DeliveryCard {
		return "", fmt.Errorf("%w: related intent %q has kind %q, not card create", ErrDependencyTerminal, relatedID, related.Kind)
	}
	if related.TurnID != intent.TurnID {
		return "", fmt.Errorf("%w: related card create %q belongs to turn %q", ErrDependencyTerminal, relatedID, related.TurnID)
	}
	switch related.Status {
	case channeltypes.DeliveryDelivered:
		messageID := strings.TrimSpace(related.MessageID)
		if messageID == "" {
			return "", fmt.Errorf("%w: delivered card create %q has no remote message ID", ErrDependencyTerminal, relatedID)
		}
		return messageID, nil
	case channeltypes.DeliveryPending, channeltypes.DeliveryDispatching:
		return "", fmt.Errorf("%w: related card create %q is %s", ErrDependencyPending, relatedID, related.Status)
	case channeltypes.DeliveryFailed:
		return "", fmt.Errorf("%w: related card create %q failed: %s", ErrDependencyTerminal, relatedID, related.LastError)
	default:
		return "", fmt.Errorf("%w: related card create %q has unsupported status %q", ErrDependencyTerminal, relatedID, related.Status)
	}
}
