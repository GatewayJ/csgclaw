package transport

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
	larkwiki "github.com/larksuite/oapi-sdk-go/v3/service/wiki/v2"
)

const (
	commentUserIDType   = "open_id"
	commentNotFoundCode = 1069307
	wholeReplyOnlyCode  = 1069302
	commentPageSize     = 100
	commentMaxPages     = 10
)

var commentMarkdownSyntax = regexp.MustCompile("[*_~`#>]")

type oapiCommentAdapter struct {
	client *lark.Client
	tokens tenantTokenSource
}

func newOAPICommentAdapter(client *lark.Client, tokens tenantTokenSource) CommentAdapter {
	return &oapiCommentAdapter{client: client, tokens: tokens}
}

func (a *oapiCommentAdapter) ResolveCommentTarget(ctx context.Context, fileToken, fileType string) (CommentTarget, bool, error) {
	base := CommentTarget{FileToken: strings.TrimSpace(fileToken), FileType: strings.ToLower(strings.TrimSpace(fileType))}
	if base.FileToken == "" || !supportedCommentFileType(base.FileType) {
		return CommentTarget{}, false, nil
	}
	if a == nil || a.client == nil {
		return CommentTarget{}, false, ErrInvalidConfig
	}
	token, err := loadTenantToken(ctx, a.tokens)
	if err != nil {
		return CommentTarget{}, false, fmt.Errorf("resolve comment target: %w", err)
	}
	resp, err := a.client.Wiki.V2.Space.GetNode(ctx, larkwiki.NewGetNodeSpaceReqBuilder().Token(base.FileToken).Build(),
		larkcore.WithTenantAccessToken(token))
	if err != nil || resp == nil || !resp.Success() || resp.Data == nil || resp.Data.Node == nil ||
		resp.Data.Node.ObjToken == nil || resp.Data.Node.ObjType == nil {
		// A non-wiki token is already a valid Drive comment target.
		return base, true, nil
	}
	target := CommentTarget{FileToken: strings.TrimSpace(*resp.Data.Node.ObjToken), FileType: strings.ToLower(strings.TrimSpace(*resp.Data.Node.ObjType))}
	if target.FileToken == "" || !supportedCommentFileType(target.FileType) {
		return CommentTarget{}, false, nil
	}
	return target, true, nil
}

func (a *oapiCommentAdapter) FetchComment(ctx context.Context, target CommentTarget, commentID string) (CommentThread, error) {
	if a == nil || a.client == nil {
		return CommentThread{}, ErrInvalidConfig
	}
	target.FileToken = strings.TrimSpace(target.FileToken)
	target.FileType = strings.TrimSpace(target.FileType)
	commentID = strings.TrimSpace(commentID)
	if target.FileToken == "" || target.FileType == "" || commentID == "" {
		return CommentThread{}, fmt.Errorf("feishu comment target and comment id are required")
	}
	token, err := loadTenantToken(ctx, a.tokens)
	if err != nil {
		return CommentThread{}, fmt.Errorf("fetch comment: %w", err)
	}
	resp, err := a.client.Drive.V1.FileComment.Get(ctx, larkdrive.NewGetFileCommentReqBuilder().
		FileToken(target.FileToken).
		CommentId(commentID).
		FileType(target.FileType).
		UserIdType(commentUserIDType).
		NeedReaction(false).
		Build(), larkcore.WithTenantAccessToken(token))
	if err != nil {
		return CommentThread{}, requestAPIError("get file comment", err)
	}
	if resp.Success() {
		if resp.Data == nil {
			return CommentThread{}, nil
		}
		return commentThread(resp.Data.Quote, resp.Data.IsWhole, resp.Data.ReplyList), nil
	}
	if resp.Code != commentNotFoundCode {
		return CommentThread{}, &APIError{Operation: "get file comment", Code: resp.Code, Message: resp.Msg}
	}
	return a.findComment(ctx, target, commentID, token)
}

func (a *oapiCommentAdapter) ReplyToComment(ctx context.Context, req ReplyCommentRequest) error {
	if a == nil || a.client == nil {
		return ErrInvalidConfig
	}
	req.Target.FileToken = strings.TrimSpace(req.Target.FileToken)
	req.Target.FileType = strings.TrimSpace(req.Target.FileType)
	req.CommentID = strings.TrimSpace(req.CommentID)
	if req.Target.FileToken == "" || req.Target.FileType == "" || req.CommentID == "" || strings.TrimSpace(req.Text) == "" {
		return fmt.Errorf("feishu comment target, comment id, and reply text are required")
	}
	text := stripCommentMarkdown(req.Text)
	if text == "" {
		return fmt.Errorf("reply to comment: text is empty after formatting")
	}
	token, err := loadTenantToken(ctx, a.tokens)
	if err != nil {
		return fmt.Errorf("reply to comment: %w", err)
	}
	if req.TopLevel {
		return a.replyTopLevel(ctx, req.Target, text, token)
	}
	resp, err := a.client.Drive.V1.FileCommentReply.Create(ctx, larkdrive.NewCreateFileCommentReplyReqBuilder().
		FileToken(req.Target.FileToken).
		CommentId(req.CommentID).
		FileType(req.Target.FileType).
		UserIdType(commentUserIDType).
		Body(larkdrive.NewCreateFileCommentReplyReqBodyBuilder().Content(commentTextContent(text)).Build()).
		Build(), larkcore.WithTenantAccessToken(token))
	if err != nil {
		return requestAPIError("create file comment reply", err)
	}
	if resp.Success() {
		return nil
	}
	if resp.Code == wholeReplyOnlyCode {
		return a.replyTopLevel(ctx, req.Target, text, token)
	}
	return &APIError{Operation: "create file comment reply", Code: resp.Code, Message: resp.Msg}
}

func (a *oapiCommentAdapter) findComment(ctx context.Context, target CommentTarget, commentID, token string) (CommentThread, error) {
	pageToken := ""
	for page := 0; page < commentMaxPages; page++ {
		builder := larkdrive.NewListFileCommentReqBuilder().
			FileToken(target.FileToken).
			FileType(target.FileType).
			UserIdType(commentUserIDType).
			NeedReaction(false).
			PageSize(commentPageSize)
		if pageToken != "" {
			builder.PageToken(pageToken)
		}
		resp, err := a.client.Drive.V1.FileComment.List(ctx, builder.Build(), larkcore.WithTenantAccessToken(token))
		if err != nil {
			return CommentThread{}, requestAPIError("list file comments", err)
		}
		if !resp.Success() {
			return CommentThread{}, &APIError{Operation: "list file comments", Code: resp.Code, Message: resp.Msg}
		}
		if resp.Data == nil {
			return CommentThread{}, nil
		}
		for _, item := range resp.Data.Items {
			if item != nil && item.CommentId != nil && *item.CommentId == commentID {
				return commentThread(item.Quote, item.IsWhole, item.ReplyList), nil
			}
		}
		if resp.Data.HasMore == nil || !*resp.Data.HasMore || resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			break
		}
		pageToken = *resp.Data.PageToken
	}
	return CommentThread{}, nil
}

func (a *oapiCommentAdapter) replyTopLevel(ctx context.Context, target CommentTarget, text, token string) error {
	resp, err := a.client.Drive.V1.FileComment.Create(ctx, larkdrive.NewCreateFileCommentReqBuilder().
		FileToken(target.FileToken).
		FileType(target.FileType).
		UserIdType(commentUserIDType).
		FileComment(larkdrive.NewFileCommentBuilder().
			ReplyList(larkdrive.NewReplyListBuilder().
				Replies([]*larkdrive.FileCommentReply{
					larkdrive.NewFileCommentReplyBuilder().Content(commentTextContent(text)).Build(),
				}).
				Build()).
			Build()).
		Build(), larkcore.WithTenantAccessToken(token))
	if err != nil {
		return requestAPIError("create top-level file comment", err)
	}
	if !resp.Success() {
		return &APIError{Operation: "create top-level file comment", Code: resp.Code, Message: resp.Msg}
	}
	return nil
}

func commentTextContent(text string) *larkdrive.ReplyContent {
	return larkdrive.NewReplyContentBuilder().
		Elements([]*larkdrive.ReplyElement{
			larkdrive.NewReplyElementBuilder().
				Type("text_run").
				TextRun(larkdrive.NewTextRunBuilder().Text(text).Build()).
				Build(),
		}).
		Build()
}

func commentThread(quote *string, whole *bool, replies *larkdrive.ReplyList) CommentThread {
	out := CommentThread{}
	if quote != nil {
		out.Quote = *quote
	}
	if whole != nil {
		out.IsWhole = *whole
	}
	if replies == nil {
		return out
	}
	for _, reply := range replies.Replies {
		if reply == nil {
			continue
		}
		mapped := CommentReply{}
		if reply.ReplyId != nil {
			mapped.ID = *reply.ReplyId
		}
		if reply.Content != nil {
			for _, element := range reply.Content.Elements {
				if element == nil {
					continue
				}
				item := CommentElement{}
				if element.Type != nil {
					item.Type = *element.Type
				}
				if element.TextRun != nil && element.TextRun.Text != nil {
					item.Text = *element.TextRun.Text
				}
				if element.DocsLink != nil && element.DocsLink.Url != nil {
					item.URL = *element.DocsLink.Url
				}
				if element.Person != nil && element.Person.UserId != nil {
					item.PersonID = *element.Person.UserId
				}
				mapped.Elements = append(mapped.Elements, item)
			}
		}
		out.Replies = append(out.Replies, mapped)
	}
	return out
}

func supportedCommentFileType(fileType string) bool {
	switch strings.ToLower(strings.TrimSpace(fileType)) {
	case "doc", "docx", "sheet", "file":
		return true
	default:
		return false
	}
}

func stripCommentMarkdown(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = commentMarkdownSyntax.ReplaceAllString(text, "")
	lines := strings.Split(text, "\n")
	for index := range lines {
		lines[index] = strings.TrimSpace(lines[index])
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

var _ CommentAdapter = (*oapiCommentAdapter)(nil)
