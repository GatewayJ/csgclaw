package context

import "testing"

func TestConversationAndTurnIdentitiesAreStableAndBindingScoped(t *testing.T) {
	t.Parallel()

	first := ChatConversationKey("binding-a", "chat-1", "")
	if first != ChatConversationKey("binding-a", "chat-1", "") {
		t.Fatal("conversation key is not deterministic")
	}
	if first == ChatConversationKey("binding-b", "chat-1", "") {
		t.Fatal("conversation key was not scoped by binding")
	}
	if scoped := DocumentCommentConversationKey("binding-a", "docx", "file-1", "comment-1"); scoped == first {
		t.Fatal("document scope unexpectedly reused chat conversation key")
	}
	turn := TurnID("binding-a", "event-1", "message-1")
	if turn != TurnID("binding-a", "event-1", "different-message") {
		t.Fatal("event ID should take precedence over message ID")
	}
}

func TestConversationKeyIgnoresRootReplyScopeWithoutThread(t *testing.T) {
	t.Parallel()

	want := ChatConversationKey("binding-a", "chat-1", "")
	got := ChatConversationKey("binding-a", "chat-1", "")
	if got != want {
		t.Fatalf("ordinary reply key = %q, want chat key %q", got, want)
	}
	thread := ChatConversationKey("binding-a", "chat-1", "thread-1")
	if thread == want {
		t.Fatal("real topic unexpectedly reused chat conversation key")
	}
}

func TestAcceptMessageRequiresExactGroupBotMention(t *testing.T) {
	t.Parallel()

	if !AcceptMessage("p2p", false, "bot", nil) {
		t.Fatal("direct message was rejected")
	}
	if AcceptMessage("group", false, "bot", []Mention{{OpenID: "someone-else"}}) {
		t.Fatal("group message mentioning another user was accepted")
	}
	if !AcceptMessage("group", false, "bot", []Mention{{OpenID: "bot"}}) {
		t.Fatal("exact bot mention was rejected")
	}
	if AcceptMessage("topic_group", false, "bot", []Mention{{OpenID: "someone-else"}}) {
		t.Fatal("topic group message mentioning another user was accepted")
	}
	if !AcceptMessage("topic_group", false, "bot", []Mention{{OpenID: "bot"}}) {
		t.Fatal("exact bot mention in topic group was rejected")
	}
	if AcceptMessage("unknown", false, "bot", []Mention{{OpenID: "bot"}}) {
		t.Fatal("unknown chat type was accepted")
	}
}

func TestStripBotMentionKeepsOtherMentions(t *testing.T) {
	t.Parallel()

	got := StripBotMention("@_user_1 ask @_user_2", "bot", []Mention{
		{Key: "@_user_1", OpenID: "bot"},
		{Key: "@_user_2", OpenID: "human"},
	})
	if got != "ask @_user_2" {
		t.Fatalf("StripBotMention() = %q", got)
	}
}
