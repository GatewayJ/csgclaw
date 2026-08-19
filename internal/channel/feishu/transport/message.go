package transport

import (
	"context"
	"fmt"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type oapiMessageAdapter struct {
	client *lark.Client
	tokens tenantTokenSource
}

func newOAPIMessageAdapter(client *lark.Client, tokens tenantTokenSource) MessageAdapter {
	return &oapiMessageAdapter{client: client, tokens: tokens}
}

func (a *oapiMessageAdapter) FetchMessage(ctx context.Context, messageID string) (Message, bool, error) {
	messageID = strings.TrimSpace(messageID)
	if ctx == nil {
		return Message{}, false, errNilContext
	}
	if a == nil || a.client == nil {
		return Message{}, false, ErrInvalidConfig
	}
	if messageID == "" {
		return Message{}, false, nil
	}
	token, err := loadTenantToken(ctx, a.tokens)
	if err != nil {
		return Message{}, false, fmt.Errorf("fetch message: %w", err)
	}
	resp, err := a.client.Im.V1.Message.Get(ctx, larkim.NewGetMessageReqBuilder().
		MessageId(messageID).
		UserIdType("open_id").
		Build(), larkcore.WithTenantAccessToken(token))
	if err != nil {
		return Message{}, false, requestAPIError("get message", err)
	}
	if resp == nil {
		return Message{}, false, missingAPIResponse("get message")
	}
	if apiResponseFailed(resp.Success(), resp.ApiResp) {
		return Message{}, false, responseAPIError("get message", resp.Code, resp.Msg, resp.ApiResp)
	}
	if resp.Data == nil {
		return Message{}, false, nil
	}
	for _, item := range resp.Data.Items {
		if item == nil {
			continue
		}
		mapped := mapOAPIMessage(item)
		if mapped.ID == messageID {
			return mapped, true, nil
		}
	}
	return Message{}, false, nil
}

func mapOAPIMessage(message *larkim.Message) Message {
	if message == nil {
		return Message{}
	}
	messageType := stringValue(message.MsgType)
	content := ""
	if message.Body != nil {
		content = stringValue(message.Body.Content)
	}
	text, raw, resources := parseMessageContent(messageType, content)
	mapped := Message{
		ID:          stringValue(message.MessageId),
		ChatID:      stringValue(message.ChatId),
		ThreadID:    stringValue(message.ThreadId),
		RootID:      stringValue(message.RootId),
		ParentID:    stringValue(message.ParentId),
		Text:        text,
		ContentType: messageType,
		RawContent:  raw,
		Resources:   resources,
		CreatedAt:   parseMillis(stringValue(message.CreateTime)),
	}
	if message.Sender != nil {
		mapped.Sender = Identity{OpenID: stringValue(message.Sender.Id), Name: stringValue(message.Sender.SenderName)}
		if strings.EqualFold(stringValue(message.Sender.SenderType), "app") {
			mapped.SenderType = SenderBot
		} else {
			mapped.SenderType = SenderType(stringValue(message.Sender.SenderType))
		}
	}
	for _, mention := range message.Mentions {
		if mention == nil {
			continue
		}
		mapped.Mentions = append(mapped.Mentions, Mention{
			Key: stringValue(mention.Key), OpenID: stringValue(mention.Id), Name: stringValue(mention.Name),
		})
	}
	return mapped
}

var _ MessageAdapter = (*oapiMessageAdapter)(nil)
