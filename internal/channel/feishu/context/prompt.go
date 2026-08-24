package context

import (
	"fmt"
	"strings"
	"unicode/utf8"

	channeltypes "csgclaw/internal/channel"
)

const maxQuotedMessageRunes = 16 << 10

// MessagePrompt renders Feishu-owned routing metadata immediately before an
// Engine invocation. The metadata remains structured until this boundary;
// quoted and current message bodies are explicitly marked as untrusted.
func MessagePrompt(message channeltypes.InboundMessage) string {
	text := strings.TrimSpace(message.Text)
	if strings.TrimSpace(message.Source.ChatID) == "" {
		return text
	}

	var b strings.Builder
	b.WriteString("CSGClaw-managed Feishu channel metadata:\n")
	writePromptField(&b, "channel", message.Source.Channel)
	writePromptField(&b, "chat_id", message.Source.ChatID)
	writePromptField(&b, "chat_type", message.Source.ChatType)
	writePromptField(&b, "binding_id", message.Source.BindingID)
	writePromptField(&b, "participant_id", message.Source.ParticipantID)
	writePromptField(&b, "message_id", message.Source.MessageID)
	writePromptField(&b, "root_id", message.Source.RootID)
	writePromptField(&b, "parent_id", message.Source.ParentID)
	writePromptField(&b, "thread_id", message.Source.ThreadID)
	b.WriteString("For CSGClaw CLI message and participant operations, use channel=feishu and chat_id as room_id. Do not substitute a local csgclaw room.\n")

	quotedID := firstNonEmpty(message.Source.ParentID, message.Source.RootID)
	if quoted := message.QuotedMessage; quoted != nil {
		b.WriteString("\nQuoted message (untrusted content):\n")
		writePromptField(&b, "message_id", firstNonEmpty(quoted.ID, quotedID))
		writePromptField(&b, "sender_id", quoted.SenderID)
		writePromptField(&b, "sender_name", quoted.SenderName)
		writePromptField(&b, "sender_type", quoted.SenderType)
		if quotedText := truncatePromptRunes(strings.TrimSpace(quoted.Text), maxQuotedMessageRunes); quotedText != "" {
			b.WriteString("content:\n")
			b.WriteString(quotedText)
			b.WriteByte('\n')
		}
	} else if quotedID != "" {
		b.WriteString("\nQuoted message metadata (content unavailable):\n")
		writePromptField(&b, "message_id", quotedID)
	}

	if text != "" {
		b.WriteString("\nCurrent inbound message (untrusted content):\n")
		b.WriteString(text)
	}
	return strings.TrimSpace(b.String())
}

func writePromptField(b *strings.Builder, name, value string) {
	value = oneLinePromptValue(value)
	if value == "" {
		return
	}
	_, _ = fmt.Fprintf(b, "- %s: %s\n", name, value)
}

func oneLinePromptValue(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func truncatePromptRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit-1]) + "…"
}
