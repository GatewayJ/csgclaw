package ingress

import (
	"context"
	"errors"
	"fmt"
	"strings"

	channeltypes "csgclaw/internal/channel"
	feishuctx "csgclaw/internal/channel/feishu/context"
	"csgclaw/internal/channel/feishu/transport"
)

var ErrCommentUnsupported = errors.New("Feishu comment intake is unavailable")

const maxCommentPromptRunes = 16 << 10

// normalizedComment is the lightweight event envelope kept in the bounded
// ingress buffer. The comment body is read outside the protocol callback.
type normalizedComment struct {
	Source          channeltypes.Source `json:"source"`
	ConversationKey string              `json:"conversation_key"`
	TurnID          string              `json:"turn_id"`
	FileToken       string              `json:"file_token"`
	FileType        string              `json:"file_type"`
	CommentID       string              `json:"comment_id"`
	ReplyID         string              `json:"reply_id"`
}

func normalizeComment(binding channeltypes.Binding, event transport.Event, bot transport.Identity) (normalizedComment, bool, error) {
	comment := event.Comment
	if comment == nil {
		return normalizedComment{}, false, fmt.Errorf("Feishu comment payload is required")
	}
	eventID := strings.TrimSpace(event.EventID)
	fileToken := strings.TrimSpace(comment.FileToken)
	fileType := strings.ToLower(strings.TrimSpace(comment.FileType))
	commentID := strings.TrimSpace(comment.CommentID)
	replyID := strings.TrimSpace(comment.ReplyID)
	if eventID == "" || fileToken == "" || fileType == "" || commentID == "" || replyID == "" {
		return normalizedComment{}, false, fmt.Errorf("Feishu comment event, file, comment, and reply IDs are required")
	}
	if strings.TrimSpace(bot.OpenID) != "" && strings.TrimSpace(comment.Operator.OpenID) == strings.TrimSpace(bot.OpenID) {
		return normalizedComment{}, false, nil
	}
	if !comment.MentionedBot {
		return normalizedComment{}, false, nil
	}
	conversationKey := feishuctx.DocumentCommentConversationKey(binding.ID, fileType, fileToken, commentID)
	turnID := feishuctx.TurnID(binding.ID, eventID, replyID)
	return normalizedComment{
		Source: channeltypes.Source{
			Channel:   binding.Channel,
			BindingID: binding.ID,
			EventID:   eventID,
			MessageID: replyID,
		},
		ConversationKey: conversationKey,
		TurnID:          turnID,
		FileToken:       fileToken,
		FileType:        fileType,
		CommentID:       commentID,
		ReplyID:         replyID,
	}, true, nil
}

func prepareCommentMessage(ctx context.Context, binding channeltypes.Binding, comments transport.CommentAdapter, comment normalizedComment) (channeltypes.InboundMessage, bool, error) {
	if comments == nil {
		return channeltypes.InboundMessage{}, false, ErrCommentUnsupported
	}
	target, accessible, err := comments.ResolveCommentTarget(ctx, comment.FileToken, comment.FileType)
	if err != nil {
		return channeltypes.InboundMessage{}, false, err
	}
	if !accessible || strings.TrimSpace(target.FileToken) == "" || strings.TrimSpace(target.FileType) == "" {
		return channeltypes.InboundMessage{}, false, nil
	}
	thread, err := comments.FetchComment(ctx, target, comment.CommentID)
	if err != nil {
		return channeltypes.InboundMessage{}, false, err
	}
	question, found := commentReplyText(thread, comment.ReplyID)
	if !found || strings.TrimSpace(question) == "" {
		return channeltypes.InboundMessage{}, false, nil
	}
	prompt := commentPrompt(target.FileToken, target.FileType, thread.Quote, question)
	return channeltypes.InboundMessage{
		Source:          comment.Source,
		AgentID:         binding.AgentID,
		ConversationKey: comment.ConversationKey,
		TurnID:          comment.TurnID,
		Text:            prompt,
		ReplyTarget: &channeltypes.ReplyTarget{
			Kind:         channeltypes.ReplyTargetComment,
			ResourceID:   strings.TrimSpace(target.FileToken),
			ResourceType: strings.TrimSpace(target.FileType),
			ParentID:     comment.CommentID,
			TopLevel:     thread.IsWhole,
		},
	}, true, nil
}

func commentReplyText(thread transport.CommentThread, replyID string) (string, bool) {
	replyID = strings.TrimSpace(replyID)
	for _, reply := range thread.Replies {
		if strings.TrimSpace(reply.ID) != replyID {
			continue
		}
		parts := make([]string, 0, len(reply.Elements))
		for _, element := range reply.Elements {
			switch strings.ToLower(strings.TrimSpace(element.Type)) {
			case "text_run", "text":
				if text := strings.TrimSpace(element.Text); text != "" {
					parts = append(parts, text)
				}
			case "docs_link", "link":
				if url := strings.TrimSpace(element.URL); url != "" {
					parts = append(parts, url)
				}
			}
		}
		return strings.Join(parts, " "), true
	}
	return "", false
}

func commentPrompt(fileToken, fileType, quote, question string) string {
	const (
		questionLabel = "\n用户的问题："
		guidance      = "\n\n如需读取正文，请使用当前可用的飞书文档工具读取对应文档；不要调用评论回复接口，渠道会负责回复。" +
			"\n最终答案请直接输出纯文本，不要包含内部思考、工具日志或 Markdown 代码块。"
		quoteLabel = "\n用户选中的原文：\n> "
	)

	prefix := "我在飞书云文档评论里被 @了。\n文档类型：" +
		truncateCommentSection(strings.TrimSpace(fileType), 64) +
		"\nfile_token：" + truncateCommentSection(strings.TrimSpace(fileToken), 512)
	fixedRunes := len([]rune(prefix + questionLabel + guidance))
	question = truncateCommentSection(strings.TrimSpace(question), maxCommentPromptRunes-fixedRunes)
	base := prefix + questionLabel + question + guidance

	quote = strings.TrimSpace(quote)
	quoteBudget := maxCommentPromptRunes - len([]rune(base+quoteLabel))
	if quote == "" || quoteBudget <= 1 {
		return base
	}
	renderedQuote := strings.ReplaceAll(quote, "\n", "\n> ")
	renderedQuote = truncateCommentSection(renderedQuote, quoteBudget)
	return prefix + quoteLabel + renderedQuote + questionLabel + question + guidance
}

func truncateCommentSection(value string, limit int) string {
	runes := []rune(value)
	if limit <= 0 {
		return ""
	}
	if len(runes) <= limit {
		return value
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}
