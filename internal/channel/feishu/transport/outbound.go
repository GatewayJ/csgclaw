package transport

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type larkOpenAPI interface {
	CreateMessage(context.Context, createMessageAPIRequest) (*larkim.CreateMessageResp, error)
	ReplyMessage(context.Context, replyMessageAPIRequest) (*larkim.ReplyMessageResp, error)
	UpdateMessage(context.Context, updateMessageAPIRequest) (*larkim.UpdateMessageResp, error)
	PatchMessage(context.Context, patchMessageAPIRequest) (*larkim.PatchMessageResp, error)
	CreateMessageReaction(context.Context, createReactionAPIRequest) (*larkim.CreateMessageReactionResp, error)
	DeleteMessageReaction(context.Context, deleteReactionAPIRequest) (*larkim.DeleteMessageReactionResp, error)
}

type createMessageAPIRequest struct {
	ReceiveIDType string
	Body          *larkim.CreateMessageReqBody
}

type replyMessageAPIRequest struct {
	MessageID string
	Body      *larkim.ReplyMessageReqBody
}

type updateMessageAPIRequest struct {
	MessageID string
	Body      *larkim.UpdateMessageReqBody
}

type patchMessageAPIRequest struct {
	MessageID string
	Body      *larkim.PatchMessageReqBody
}

type createReactionAPIRequest struct {
	MessageID string
	Body      *larkim.CreateMessageReactionReqBody
}

type deleteReactionAPIRequest struct {
	MessageID  string
	ReactionID string
}

const (
	feishuTextRequestBodyLimit = 150 << 10
	feishuRichRequestBodyLimit = 30 << 10
)

// directOutbound maps one delivery attempt to exactly one generated
// OpenAPI method call. It never invokes the SDK channel Send helper, whose
// retry, fallback, and chunking policy sits above the raw message APIs.
type directOutbound struct {
	api larkOpenAPI
}

func newDirectOutbound(client *lark.Client, tokens tenantTokenSource) *directOutbound {
	return &directOutbound{api: &sdkLarkOpenAPI{client: client, tokens: tokens}}
}

func newDirectOutboundWithAPI(api larkOpenAPI) *directOutbound {
	return &directOutbound{api: api}
}

func (o *directOutbound) SendText(ctx context.Context, req SendTextRequest) (SendResult, error) {
	if err := validateDirectSend(ctx, req.ChatID, req.IdempotencyKey, req.ReplyTo, req.ReplyInThread, req.ThreadID); err != nil {
		return SendResult{}, err
	}
	if req.Text == "" {
		return SendResult{}, errors.New("feishu send text content is required")
	}
	msgType := "text"
	content, err := json.Marshal(map[string]string{"text": req.Text})
	if err != nil {
		return SendResult{}, fmt.Errorf("encode feishu text: %w", err)
	}
	if req.Markdown {
		msgType = "post"
		post, err := json.Marshal(feishuMarkdownPost{
			ZhCN: feishuPostLanguage{Content: [][]feishuPostElement{{{Tag: "md", Text: req.Text}}}},
		})
		if err != nil {
			return SendResult{}, fmt.Errorf("encode feishu markdown: %w", err)
		}
		content = post
	}
	return o.sendMessage(ctx, req.ChatID, req.ReplyTo, req.ReplyInThread, req.IdempotencyKey, msgType, string(content))
}

type feishuMarkdownPost struct {
	ZhCN feishuPostLanguage `json:"zh_cn"`
}

type feishuPostLanguage struct {
	Content [][]feishuPostElement `json:"content"`
}

type feishuPostElement struct {
	Tag  string `json:"tag"`
	Text string `json:"text"`
}

func (o *directOutbound) SendCard(ctx context.Context, req SendCardRequest) (SendResult, error) {
	if err := validateDirectSend(ctx, req.ChatID, req.IdempotencyKey, req.ReplyTo, req.ReplyInThread, req.ThreadID); err != nil {
		return SendResult{}, err
	}
	if req.Card == nil {
		return SendResult{}, errors.New("feishu card is required")
	}
	content, err := json.Marshal(req.Card)
	if err != nil {
		return SendResult{}, fmt.Errorf("encode feishu card: %w", err)
	}
	return o.sendMessage(ctx, req.ChatID, req.ReplyTo, req.ReplyInThread, req.IdempotencyKey, "interactive", string(content))
}

func (o *directOutbound) UpdateText(ctx context.Context, req UpdateTextRequest) error {
	if ctx == nil {
		return errNilContext
	}
	if o == nil || o.api == nil {
		return ErrInvalidConfig
	}
	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" {
		return errors.New("feishu update text message id is required")
	}
	if req.Text == "" {
		return errors.New("feishu update text content is required")
	}
	msgType := "text"
	content, err := json.Marshal(map[string]string{"text": req.Text})
	if err != nil {
		return fmt.Errorf("encode feishu text update: %w", err)
	}
	if req.Markdown {
		msgType = "post"
		content, err = json.Marshal(feishuMarkdownPost{
			ZhCN: feishuPostLanguage{Content: [][]feishuPostElement{{{Tag: "md", Text: req.Text}}}},
		})
		if err != nil {
			return fmt.Errorf("encode feishu markdown update: %w", err)
		}
	}
	// Feishu's edit-message API (PUT) owns text/post updates. Patch is the card
	// update API and does not carry msg_type; using it for a post can leave the
	// original "thinking" message unchanged.
	body := larkim.NewUpdateMessageReqBodyBuilder().MsgType(msgType).Content(string(content)).Build()
	if err := validateMessageRequestSize("update text", msgType, body); err != nil {
		return err
	}
	resp, err := o.api.UpdateMessage(ctx, updateMessageAPIRequest{MessageID: messageID, Body: body})
	if err != nil {
		return requestAPIError("update message", err)
	}
	if resp == nil {
		return missingAPIResponse("update message")
	}
	if apiResponseFailed(resp.Success(), resp.ApiResp) {
		return responseAPIError("update message", resp.Code, resp.Msg, resp.ApiResp)
	}
	return nil
}

func (o *directOutbound) UpdateCard(ctx context.Context, req UpdateCardRequest) error {
	if ctx == nil {
		return errNilContext
	}
	if o == nil || o.api == nil {
		return ErrInvalidConfig
	}
	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" {
		return errors.New("feishu update card message id is required")
	}
	if req.Card == nil {
		return errors.New("feishu card is required")
	}
	content, err := json.Marshal(req.Card)
	if err != nil {
		return fmt.Errorf("encode feishu card update: %w", err)
	}
	body := larkim.NewPatchMessageReqBodyBuilder().Content(string(content)).Build()
	if err := validateMessageRequestSize("patch card", "interactive", body); err != nil {
		return err
	}
	resp, err := o.api.PatchMessage(ctx, patchMessageAPIRequest{
		MessageID: messageID,
		Body:      body,
	})
	if err != nil {
		return requestAPIError("patch message", err)
	}
	if resp == nil {
		return missingAPIResponse("patch message")
	}
	if apiResponseFailed(resp.Success(), resp.ApiResp) {
		return responseAPIError("patch message", resp.Code, resp.Msg, resp.ApiResp)
	}
	return nil
}

func (o *directOutbound) AddReaction(ctx context.Context, req AddReactionRequest) (AddReactionResult, error) {
	if ctx == nil {
		return AddReactionResult{}, errNilContext
	}
	if o == nil || o.api == nil {
		return AddReactionResult{}, ErrInvalidConfig
	}
	messageID := strings.TrimSpace(req.MessageID)
	emojiType := strings.TrimSpace(req.EmojiType)
	if messageID == "" {
		return AddReactionResult{}, errors.New("feishu reaction message id is required")
	}
	if emojiType == "" {
		return AddReactionResult{}, errors.New("feishu reaction emoji type is required")
	}
	resp, err := o.api.CreateMessageReaction(ctx, createReactionAPIRequest{
		MessageID: messageID,
		Body: larkim.NewCreateMessageReactionReqBodyBuilder().
			ReactionType(larkim.NewEmojiBuilder().EmojiType(emojiType).Build()).
			Build(),
	})
	if err != nil {
		return AddReactionResult{}, requestAPIError("create message reaction", err)
	}
	if resp == nil {
		return AddReactionResult{}, missingAPIResponse("create message reaction")
	}
	if apiResponseFailed(resp.Success(), resp.ApiResp) {
		return AddReactionResult{}, responseAPIError("create message reaction", resp.Code, resp.Msg, resp.ApiResp)
	}
	if resp.Data == nil || resp.Data.ReactionId == nil || strings.TrimSpace(*resp.Data.ReactionId) == "" {
		return AddReactionResult{}, missingAPIResult("create message reaction", "reaction_id", resp.ApiResp)
	}
	return AddReactionResult{ReactionID: strings.TrimSpace(*resp.Data.ReactionId)}, nil
}

func (o *directOutbound) DeleteReaction(ctx context.Context, req DeleteReactionRequest) error {
	if ctx == nil {
		return errNilContext
	}
	if o == nil || o.api == nil {
		return ErrInvalidConfig
	}
	messageID := strings.TrimSpace(req.MessageID)
	reactionID := strings.TrimSpace(req.ReactionID)
	if messageID == "" {
		return errors.New("feishu reaction message id is required")
	}
	if reactionID == "" {
		return errors.New("feishu reaction id is required")
	}
	resp, err := o.api.DeleteMessageReaction(ctx, deleteReactionAPIRequest{MessageID: messageID, ReactionID: reactionID})
	if err != nil {
		return requestAPIError("delete message reaction", err)
	}
	if resp == nil {
		return missingAPIResponse("delete message reaction")
	}
	if apiResponseFailed(resp.Success(), resp.ApiResp) {
		return responseAPIError("delete message reaction", resp.Code, resp.Msg, resp.ApiResp)
	}
	return nil
}

func (o *directOutbound) sendMessage(ctx context.Context, chatID, replyTo string, replyInThread bool, idempotencyKey, msgType, content string) (SendResult, error) {
	if o == nil || o.api == nil {
		return SendResult{}, ErrInvalidConfig
	}
	uuid := feishuMessageUUID(idempotencyKey)
	replyTo = strings.TrimSpace(replyTo)
	if replyTo != "" {
		bodyBuilder := larkim.NewReplyMessageReqBodyBuilder().
			MsgType(msgType).
			Content(content).
			Uuid(uuid)
		if replyInThread {
			bodyBuilder.ReplyInThread(true)
		}
		body := bodyBuilder.Build()
		if err := validateMessageRequestSize("reply message", msgType, body); err != nil {
			return SendResult{}, err
		}
		resp, err := o.api.ReplyMessage(ctx, replyMessageAPIRequest{MessageID: replyTo, Body: body})
		if err != nil {
			return SendResult{}, requestAPIError("reply message", err)
		}
		if resp == nil {
			return SendResult{}, missingAPIResponse("reply message")
		}
		if apiResponseFailed(resp.Success(), resp.ApiResp) {
			return SendResult{}, responseAPIError("reply message", resp.Code, resp.Msg, resp.ApiResp)
		}
		if resp.Data == nil || resp.Data.MessageId == nil || strings.TrimSpace(*resp.Data.MessageId) == "" {
			return SendResult{}, missingAPIResult("reply message", "message_id", resp.ApiResp)
		}
		return SendResult{MessageID: strings.TrimSpace(*resp.Data.MessageId)}, nil
	}

	body := larkim.NewCreateMessageReqBodyBuilder().
		ReceiveId(strings.TrimSpace(chatID)).
		MsgType(msgType).
		Content(content).
		Uuid(uuid).
		Build()
	if err := validateMessageRequestSize("create message", msgType, body); err != nil {
		return SendResult{}, err
	}
	resp, err := o.api.CreateMessage(ctx, createMessageAPIRequest{
		ReceiveIDType: "chat_id",
		Body:          body,
	})
	if err != nil {
		return SendResult{}, requestAPIError("create message", err)
	}
	if resp == nil {
		return SendResult{}, missingAPIResponse("create message")
	}
	if apiResponseFailed(resp.Success(), resp.ApiResp) {
		return SendResult{}, responseAPIError("create message", resp.Code, resp.Msg, resp.ApiResp)
	}
	if resp.Data == nil || resp.Data.MessageId == nil || strings.TrimSpace(*resp.Data.MessageId) == "" {
		return SendResult{}, missingAPIResult("create message", "message_id", resp.ApiResp)
	}
	return SendResult{MessageID: strings.TrimSpace(*resp.Data.MessageId)}, nil
}

// validateMessageRequestSize measures the exact JSON body handed to the SDK.
// It rejects an invalid attempt instead of splitting, downgrading, or relying
// on a remote 4xx. Feishu limits text bodies to 150 KiB and rich/card bodies to
// 30 KiB.
func validateMessageRequestSize(operation, msgType string, body any) error {
	wireBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode feishu %s request: %w", operation, err)
	}
	limit := feishuRichRequestBodyLimit
	if msgType == "text" {
		limit = feishuTextRequestBodyLimit
	}
	if len(wireBody) > limit {
		return fmt.Errorf("feishu %s request body is %d bytes; limit is %d: %w", operation, len(wireBody), limit, ErrPayloadTooLarge)
	}
	return nil
}

func validateDirectSend(ctx context.Context, chatID, idempotencyKey, replyTo string, replyInThread bool, threadID string) error {
	if ctx == nil {
		return errNilContext
	}
	if strings.TrimSpace(chatID) == "" {
		return errors.New("feishu send chat id is required")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return errors.New("feishu send idempotency key is required")
	}
	replyTo = strings.TrimSpace(replyTo)
	if replyInThread && replyTo == "" {
		return errors.New("feishu threaded reply target is required")
	}
	if strings.TrimSpace(threadID) != "" && replyTo == "" {
		return errors.New("feishu thread id requires a reply target")
	}
	return nil
}

// feishuMessageUUID hashes the delivery intent ID into an RFC 4122-compatible
// version-8 UUID. The raw (potentially long or sensitive) intent ID is never
// sent to Feishu.
func feishuMessageUUID(idempotencyKey string) string {
	value := sha256.Sum256([]byte(strings.TrimSpace(idempotencyKey)))
	value[6] = (value[6] & 0x0f) | 0x80
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

func requestAPIError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return err
	}
	return &APIError{Operation: operation, Message: "request failed", cause: err}
}

func responseAPIError(operation string, code int, message string, response *larkcore.ApiResp) error {
	return &APIError{
		Operation:  operation,
		Code:       code,
		HTTPStatus: apiResponseStatus(response),
		Message:    sanitizeAPIMessage(message),
	}
}

func missingAPIResponse(operation string) error {
	return &APIError{Operation: operation, Message: "response was empty"}
}

func missingAPIResult(operation, field string, response *larkcore.ApiResp) error {
	return &APIError{Operation: operation, HTTPStatus: apiResponseStatus(response), Message: field + " was missing from the response"}
}

func apiResponseStatus(response *larkcore.ApiResp) int {
	if response == nil {
		return 0
	}
	return response.StatusCode
}

func apiResponseFailed(success bool, response *larkcore.ApiResp) bool {
	status := apiResponseStatus(response)
	return !success || (status != 0 && (status < 200 || status >= 300))
}

type sdkLarkOpenAPI struct {
	client *lark.Client
	tokens tenantTokenSource
}

func (a *sdkLarkOpenAPI) CreateMessage(ctx context.Context, req createMessageAPIRequest) (*larkim.CreateMessageResp, error) {
	token, err := a.token(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(req.ReceiveIDType).
		Body(req.Body).
		Build(), larkcore.WithTenantAccessToken(token))
	if resp != nil {
		a.invalidateRejectedToken(token, resp.Code)
	}
	return resp, err
}

func (a *sdkLarkOpenAPI) ReplyMessage(ctx context.Context, req replyMessageAPIRequest) (*larkim.ReplyMessageResp, error) {
	token, err := a.token(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Im.V1.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
		MessageId(req.MessageID).
		Body(req.Body).
		Build(), larkcore.WithTenantAccessToken(token))
	if resp != nil {
		a.invalidateRejectedToken(token, resp.Code)
	}
	return resp, err
}

func (a *sdkLarkOpenAPI) PatchMessage(ctx context.Context, req patchMessageAPIRequest) (*larkim.PatchMessageResp, error) {
	token, err := a.token(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Im.V1.Message.Patch(ctx, larkim.NewPatchMessageReqBuilder().
		MessageId(req.MessageID).
		Body(req.Body).
		Build(), larkcore.WithTenantAccessToken(token))
	if resp != nil {
		a.invalidateRejectedToken(token, resp.Code)
	}
	return resp, err
}

func (a *sdkLarkOpenAPI) UpdateMessage(ctx context.Context, req updateMessageAPIRequest) (*larkim.UpdateMessageResp, error) {
	token, err := a.token(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Im.V1.Message.Update(ctx, larkim.NewUpdateMessageReqBuilder().
		MessageId(req.MessageID).
		Body(req.Body).
		Build(), larkcore.WithTenantAccessToken(token))
	if resp != nil {
		a.invalidateRejectedToken(token, resp.Code)
	}
	return resp, err
}

func (a *sdkLarkOpenAPI) CreateMessageReaction(ctx context.Context, req createReactionAPIRequest) (*larkim.CreateMessageReactionResp, error) {
	token, err := a.token(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Im.V1.MessageReaction.Create(ctx, larkim.NewCreateMessageReactionReqBuilder().
		MessageId(req.MessageID).
		Body(req.Body).
		Build(), larkcore.WithTenantAccessToken(token))
	if resp != nil {
		a.invalidateRejectedToken(token, resp.Code)
	}
	return resp, err
}

func (a *sdkLarkOpenAPI) DeleteMessageReaction(ctx context.Context, req deleteReactionAPIRequest) (*larkim.DeleteMessageReactionResp, error) {
	token, err := a.token(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Im.V1.MessageReaction.Delete(ctx, larkim.NewDeleteMessageReactionReqBuilder().
		MessageId(req.MessageID).
		ReactionId(req.ReactionID).
		Build(), larkcore.WithTenantAccessToken(token))
	if resp != nil {
		a.invalidateRejectedToken(token, resp.Code)
	}
	return resp, err
}

func (a *sdkLarkOpenAPI) token(ctx context.Context) (string, error) {
	if a == nil || a.client == nil || a.tokens == nil {
		return "", ErrInvalidConfig
	}
	return a.tokens.Token(ctx)
}

func (a *sdkLarkOpenAPI) invalidateRejectedToken(token string, code int) {
	if code != tenantAccessTokenInvalidCode {
		return
	}
	if invalidatable, ok := a.tokens.(invalidatableTenantTokenSource); ok {
		invalidatable.Invalidate(token)
	}
}

var _ larkOperations = (*directOutbound)(nil)
var _ larkOpenAPI = (*sdkLarkOpenAPI)(nil)
