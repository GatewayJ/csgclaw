package execution

import (
	"fmt"
	"strings"

	"csgclaw/internal/channel/feishu/presentation"
)

func cardCreateID(turnID string) string {
	return strings.TrimSpace(turnID) + ":card:create"
}

func presentationCreateID(mode presentation.Mode, turnID string) string {
	turnID = strings.TrimSpace(turnID)
	if presentation.NormalizeMode(string(mode)) == presentation.ModeCard {
		return cardCreateID(turnID)
	}
	return turnID + ":markdown:create"
}

func presentationUpdateID(mode presentation.Mode, turnID string, sequence uint64, final bool) string {
	turnID = strings.TrimSpace(turnID)
	mode = presentation.NormalizeMode(string(mode))
	if final {
		return fmt.Sprintf("%s:%s:final", turnID, mode)
	}
	return fmt.Sprintf("%s:%s:update:%020d", turnID, mode, sequence)
}

func markdownContinuationCreateID(turnID string, part int) string {
	return fmt.Sprintf("%s:markdown:part:%06d:create", strings.TrimSpace(turnID), part)
}

func markdownContinuationUpdateID(turnID string, part int, sequence uint64, final bool) string {
	prefix := fmt.Sprintf("%s:markdown:part:%06d", strings.TrimSpace(turnID), part)
	if final {
		return prefix + ":final"
	}
	return fmt.Sprintf("%s:update:%020d", prefix, sequence)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
