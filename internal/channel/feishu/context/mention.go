package context

import "strings"

// Mention is the normalized subset used by the channel admission policy.
type Mention struct {
	Key    string
	OpenID string
}

// AcceptMessage keeps direct messages open and requires an exact mention of
// this binding's bot in group chats. It deliberately does not accept "any @".
func AcceptMessage(chatType string, mentionedBot bool, botOpenID string, mentions []Mention) bool {
	switch strings.ToLower(strings.TrimSpace(chatType)) {
	case "p2p":
		return true
	case "group", "topic_group":
		// Both ordinary groups and topic-mode groups require an exact bot
		// mention. Feishu reports them as distinct chat_type values.
	default:
		return false
	}
	if mentionedBot {
		return true
	}
	botOpenID = strings.TrimSpace(botOpenID)
	if botOpenID == "" {
		return false
	}
	for _, mention := range mentions {
		if strings.TrimSpace(mention.OpenID) == botOpenID {
			return true
		}
	}
	return false
}

// StripBotMention removes only placeholders that refer to this bot. Other
// mentions remain part of the prompt context.
func StripBotMention(text, botOpenID string, mentions []Mention) string {
	botOpenID = strings.TrimSpace(botOpenID)
	if botOpenID == "" {
		return strings.TrimSpace(text)
	}
	for _, mention := range mentions {
		if strings.TrimSpace(mention.OpenID) != botOpenID {
			continue
		}
		if key := strings.TrimSpace(mention.Key); key != "" {
			text = strings.ReplaceAll(text, key, "")
		}
	}
	return strings.TrimSpace(text)
}
