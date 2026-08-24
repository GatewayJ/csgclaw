package context

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ChatConversationKey returns a stable Engine conversation identity for a
// chat or topic. RootID is deliberately excluded because ordinary quoted
// replies also carry it; only ThreadID denotes a real topic.
func ChatConversationKey(bindingID, chatID, threadID string) string {
	chatID = strings.TrimSpace(chatID)
	threadID = strings.TrimSpace(threadID)
	scope := chatID
	if chatID != "" && threadID != "" {
		scope += ":" + threadID
	}
	return opaqueKey("feishu-conversation", bindingID, scope)
}

// DocumentCommentConversationKey returns a stable Engine conversation
// identity for one document comment thread.
func DocumentCommentConversationKey(bindingID, fileType, fileToken, commentID string) string {
	scope := strings.ToLower(strings.TrimSpace(fileType)) + ":" +
		strings.TrimSpace(fileToken) + ":" + strings.TrimSpace(commentID)
	return opaqueKey("feishu-conversation", bindingID, scope)
}

// TurnID deterministically identifies one inbound Feishu event. Redelivery of
// the same source event therefore addresses the same channel-side Turn record
// and the same Engine Turn while the Engine replay cache remains available.
func TurnID(bindingID, eventID, messageID string) string {
	return opaqueKey("feishu-turn", bindingID, firstNonEmpty(eventID, messageID))
}

func opaqueKey(namespace string, values ...string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strings.TrimSpace(namespace)))
	for _, value := range values {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(strings.TrimSpace(value)))
	}
	return strings.TrimSpace(namespace) + ":" + hex.EncodeToString(h.Sum(nil))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
