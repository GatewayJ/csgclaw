package delivery

import (
	"context"

	channeltypes "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/transport"
)

func deliverCommentReply(ctx context.Context, adapter transport.CommentAdapter, intent channeltypes.DeliveryIntent) (channeltypes.DeliveryIntent, error) {
	err := adapter.ReplyToComment(ctx, transport.ReplyCommentRequest{
		Target: transport.CommentTarget{
			FileToken: intent.ResourceID,
			FileType:  intent.ResourceType,
		},
		CommentID: intent.ParentID,
		Text:      intent.Text,
		TopLevel:  intent.TopLevel,
	})
	return intent, err
}
