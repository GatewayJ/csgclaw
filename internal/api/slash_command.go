package api

import (
	"fmt"

	"csgclaw/internal/slashcommand"
)

func normalizeSlashCommandContent(content string) (string, error) {
	normalized, ok, err := slashcommand.Normalize(content)
	if err != nil {
		return "", fmt.Errorf("invalid slash command: %w", err)
	}
	if ok {
		return normalized, nil
	}
	return content, nil
}

func normalizeFeishuSlashCommandContent(content string) (string, error) {
	normalized, ok, err := slashcommand.Normalize(content)
	if err != nil {
		return "", fmt.Errorf("invalid slash command: %w", err)
	}
	if ok {
		return normalized, nil
	}
	normalized, ok, err = slashcommand.NormalizeFeishuInput(content)
	if err != nil {
		return "", fmt.Errorf("invalid Feishu slash command: %w", err)
	}
	if ok {
		return normalized, nil
	}
	return content, nil
}
