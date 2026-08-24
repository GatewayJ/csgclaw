package delivery

import (
	"context"
	"fmt"
	"strings"

	channeltypes "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/transport"
)

func deliverReactionAdd(ctx context.Context, adapter transport.Adapter, intent channeltypes.DeliveryIntent) (channeltypes.DeliveryIntent, error) {
	result, err := adapter.AddReaction(ctx, transport.AddReactionRequest{MessageID: intent.MessageID, EmojiType: intent.EmojiType})
	if err != nil {
		return intent, err
	}
	intent.ReactionID = result.ReactionID
	return intent, nil
}

func (d *Dispatcher) deliverReactionDelete(ctx context.Context, intent channeltypes.DeliveryIntent) (channeltypes.DeliveryIntent, error) {
	reactionID := strings.TrimSpace(intent.ReactionID)
	if reactionID == "" && strings.TrimSpace(intent.RelatedID) != "" {
		related, ok := d.state.Intent(intent.RelatedID)
		if !ok || related.Kind != channeltypes.DeliveryReactionAdd || related.TurnID != intent.TurnID {
			return intent, fmt.Errorf("%w: processing reaction %q is invalid", ErrDependencyTerminal, intent.RelatedID)
		}
		switch related.Status {
		case channeltypes.DeliveryPending, channeltypes.DeliveryDispatching:
			return intent, fmt.Errorf("%w: processing reaction %q is %s", ErrDependencyPending, intent.RelatedID, related.Status)
		case channeltypes.DeliveryFailed:
			// The add never produced an addressable ReactionID, so cleanup is a
			// successful no-op rather than an endlessly retrying dependency.
			return intent, nil
		case channeltypes.DeliveryDelivered:
			if strings.TrimSpace(related.ReactionID) == "" {
				return intent, fmt.Errorf("%w: processing reaction %q has no remote ID", ErrDependencyTerminal, intent.RelatedID)
			}
		default:
			return intent, fmt.Errorf("%w: processing reaction %q has status %q", ErrDependencyTerminal, intent.RelatedID, related.Status)
		}
		reactionID = strings.TrimSpace(related.ReactionID)
	}
	if reactionID == "" {
		return intent, fmt.Errorf("Feishu reaction ID is required for cleanup")
	}
	if err := d.adapter.DeleteReaction(ctx, transport.DeleteReactionRequest{MessageID: intent.MessageID, ReactionID: reactionID}); err != nil {
		return intent, err
	}
	intent.ReactionID = reactionID
	return intent, nil
}
