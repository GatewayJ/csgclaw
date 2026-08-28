package ingress

import (
	"context"
	"strings"
	"testing"

	channeltypes "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/transport"
)

type fakeComments struct {
	target transport.CommentTarget
	ok     bool
	thread transport.CommentThread
}

func (f fakeComments) ResolveCommentTarget(context.Context, string, string) (transport.CommentTarget, bool, error) {
	return f.target, f.ok, nil
}

func (f fakeComments) FetchComment(context.Context, transport.CommentTarget, string) (transport.CommentThread, error) {
	return f.thread, nil
}

func (fakeComments) ReplyToComment(context.Context, transport.ReplyCommentRequest) error { return nil }

func TestNormalizeCommentRequiresBotMentionAndSkipsSelf(t *testing.T) {
	binding := channeltypes.Binding{ID: "binding-1", Channel: "feishu", AgentID: "agent-1"}
	event := transport.Event{Kind: transport.EventComment, EventID: "event-1", Comment: &transport.Comment{
		FileToken: "file-1", FileType: "docx", CommentID: "comment-1", ReplyID: "reply-1",
		Operator: transport.Identity{OpenID: "user-1"}, MentionedBot: true,
	}}
	comment, accepted, err := normalizeComment(binding, event, transport.Identity{OpenID: "bot-1"})
	if err != nil || !accepted {
		t.Fatalf("normalizeComment() = %#v, %t, %v", comment, accepted, err)
	}
	if comment.Source.MessageID != "reply-1" || comment.ConversationKey == "" || comment.TurnID == "" {
		t.Fatalf("normalized comment = %#v", comment)
	}

	event.Comment.MentionedBot = false
	if _, accepted, err := normalizeComment(binding, event, transport.Identity{OpenID: "bot-1"}); err != nil || accepted {
		t.Fatalf("unmentioned comment accepted=%t err=%v", accepted, err)
	}
	event.Comment.MentionedBot = true
	event.Comment.Operator.OpenID = "bot-1"
	if _, accepted, err := normalizeComment(binding, event, transport.Identity{OpenID: "bot-1"}); err != nil || accepted {
		t.Fatalf("self comment accepted=%t err=%v", accepted, err)
	}
}

func TestPrepareCommentMessageUsesExactReplyAndAuthorizedTarget(t *testing.T) {
	binding := channeltypes.Binding{ID: "binding-1", Channel: "feishu", AgentID: "agent-1"}
	comment := normalizedComment{
		Source:          channeltypes.Source{Channel: "feishu", BindingID: "binding-1", EventID: "event-1", MessageID: "reply-2"},
		ConversationKey: "conversation-1", TurnID: "turn-1", FileToken: "wiki-1", FileType: "wiki",
		CommentID: "comment-1", ReplyID: "reply-2",
	}
	comments := fakeComments{
		target: transport.CommentTarget{FileToken: "docx-1", FileType: "docx"}, ok: true,
		thread: transport.CommentThread{Quote: "quoted paragraph", IsWhole: true, Replies: []transport.CommentReply{
			{ID: "reply-1", Elements: []transport.CommentElement{{Type: "text_run", Text: "old"}}},
			{ID: "reply-2", Elements: []transport.CommentElement{{Type: "text_run", Text: "question"}, {Type: "docs_link", URL: "https://example.test/doc"}}},
		}},
	}
	message, executable, err := prepareCommentMessage(context.Background(), binding, comments, comment)
	if err != nil || !executable {
		t.Fatalf("prepareCommentMessage() = %#v, %t, %v", message, executable, err)
	}
	if message.AgentID != "agent-1" || message.ReplyTarget == nil ||
		message.ReplyTarget.Kind != channeltypes.ReplyTargetComment || message.ReplyTarget.ResourceID != "docx-1" ||
		message.ReplyTarget.ParentID != "comment-1" || !message.ReplyTarget.TopLevel {
		t.Fatalf("message target = %#v", message)
	}
	if !strings.Contains(message.Text, "quoted paragraph") || !strings.Contains(message.Text, "question") ||
		!strings.Contains(message.Text, "https://example.test/doc") || strings.Contains(message.Text, "old") {
		t.Fatalf("message text = %q", message.Text)
	}
}

func TestCommentPromptTruncatesQuoteBeforeQuestionAndGuidance(t *testing.T) {
	t.Parallel()

	prompt := commentPrompt("file-1", "docx", strings.Repeat("很长的引用\n", maxCommentPromptRunes), "必须保留的问题")
	if len([]rune(prompt)) > maxCommentPromptRunes {
		t.Fatalf("prompt runes = %d, limit = %d", len([]rune(prompt)), maxCommentPromptRunes)
	}
	for _, required := range []string{
		"用户的问题：必须保留的问题",
		"lark-cli docs +fetch --api-version v2 --doc file-1 --doc-format markdown",
		"不要调用评论回复接口，渠道会负责回复",
		"最终答案请直接输出纯文本",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("prompt missing required suffix %q: %q", required, prompt)
		}
	}
}

func TestCommentPromptUsesFileTypeSpecificLarkCLIInstructions(t *testing.T) {
	docx := commentPrompt("docx-token", "docx", "", "读正文")
	if !strings.Contains(docx, "lark-cli docs +fetch --api-version v2 --doc docx-token --doc-format markdown") {
		t.Fatalf("docx prompt missing docs fetch command: %q", docx)
	}

	sheet := commentPrompt("sheet-token", "sheet", "", "读表格")
	if !strings.Contains(sheet, "不要使用 `lark-cli docs +fetch`") ||
		!strings.Contains(sheet, "未读取表格全文") {
		t.Fatalf("sheet prompt missing sheet guidance: %q", sheet)
	}
}
