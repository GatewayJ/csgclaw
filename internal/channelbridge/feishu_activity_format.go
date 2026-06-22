package channelbridge

import (
	"encoding/json"
	"fmt"
	"strings"

	"csgclaw/internal/channelbridge/runtimebridge"
)

type feishuActivityMessage struct {
	Type    string                `json:"type"`
	Channel string                `json:"channel"`
	EventID string                `json:"event_id"`
	Content feishuActivityContent `json:"content"`
}

type feishuActivityContent struct {
	MsgType string               `json:"msgtype"`
	Body    string               `json:"body"`
	Tool    feishuActivityTool   `json:"tool"`
	Action  feishuActivityAction `json:"action"`
}

type feishuActivityTool struct {
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	InputSummary  string `json:"input_summary"`
	OutputSummary string `json:"output_summary"`
}

type feishuActivityAction struct {
	ID          string                  `json:"id"`
	Kind        string                  `json:"kind"`
	Title       string                  `json:"title"`
	Status      string                  `json:"status"`
	RequestedAt string                  `json:"requested_at"`
	ExpiresAt   string                  `json:"expires_at"`
	Options     []feishuActivityOption  `json:"options"`
	Decision    *feishuActivityDecision `json:"decision"`
}

type feishuActivityOption struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Scope string `json:"scope"`
}

type feishuActivityDecision struct {
	OptionID string `json:"option_id"`
	Kind     string `json:"kind"`
}

type feishuBridgeFormattedMessage struct {
	Text string
	Card string
}

func formatFeishuBridgeMessage(text string) feishuBridgeFormattedMessage {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return feishuBridgeFormattedMessage{Text: text}
	}

	var msg feishuActivityMessage
	if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
		return feishuBridgeFormattedMessage{Text: text}
	}
	if msg.Type != runtimebridge.AgentActivityType {
		return feishuBridgeFormattedMessage{Text: text}
	}
	switch msg.Content.MsgType {
	case runtimebridge.AgentToolMsgType:
		if msg.Content.Tool.empty() {
			return feishuBridgeFormattedMessage{Text: firstNonEmpty(msg.Content.Body, text)}
		}
		return feishuBridgeFormattedMessage{Text: renderFeishuToolActivity(msg.Content.Tool, msg.Content.Body)}
	case runtimebridge.AgentActionMsgType:
		if card := renderFeishuPermissionActivityCard(msg); card != "" {
			return feishuBridgeFormattedMessage{
				Text: firstNonEmpty(msg.Content.Body, text),
				Card: card,
			}
		}
		return feishuBridgeFormattedMessage{Text: renderFeishuActionActivity(msg.Content.Action, msg.Content.Body)}
	default:
		return feishuBridgeFormattedMessage{Text: firstNonEmpty(msg.Content.Body, text)}
	}
}

func renderFeishuToolActivity(tool feishuActivityTool, body string) string {
	var b strings.Builder
	b.WriteString(feishuToolStatusLabel(tool.Status))
	b.WriteString(" · ")
	b.WriteString(firstNonEmpty(tool.Title, tool.Kind, "Run tool"))

	sections := 0
	if command := feishuSummaryValue(tool.InputSummary, "command", "cmd"); command != "" {
		appendFeishuCodeSection(&b, "Command", command)
		sections++
	} else if input := feishuSummaryValue(tool.InputSummary, "input", "query", "path", "file", "filename"); input != "" {
		appendFeishuCodeSection(&b, "Input", input)
		sections++
	}
	if output := feishuSummaryValue(tool.OutputSummary, "output", "result", "stdout", "stderr", "error"); output != "" {
		appendFeishuCodeSection(&b, "Result", output)
		sections++
	}
	if sections == 0 {
		if body = strings.TrimSpace(body); body != "" {
			b.WriteString("\n\n")
			b.WriteString(body)
		}
	}
	return strings.TrimSpace(b.String())
}

func renderFeishuActionActivity(action feishuActivityAction, body string) string {
	title := firstNonEmpty(action.Title, action.Kind, "Permission request")
	switch strings.ToLower(strings.TrimSpace(action.Status)) {
	case "pending":
		return strings.TrimSpace("🔐 Permission required · " + title)
	case "allowed":
		return strings.TrimSpace("✅ Permission allowed · " + title)
	case "rejected":
		return strings.TrimSpace("❌ Permission rejected · " + title)
	case "expired":
		return strings.TrimSpace("⌛ Permission expired · " + title)
	case "canceled", "cancelled":
		return strings.TrimSpace("⏹️ Permission canceled · " + title)
	default:
		return firstNonEmpty(strings.TrimSpace(body), title)
	}
}

func renderFeishuPermissionActivityCard(msg feishuActivityMessage) string {
	card := renderFeishuPermissionActivityCardData(msg)
	if card == nil {
		return ""
	}
	data, err := json.Marshal(card)
	if err != nil {
		return ""
	}
	return string(data)
}

func renderFeishuPermissionActivityCardData(msg feishuActivityMessage) map[string]any {
	action := msg.Content.Action
	if strings.ToLower(strings.TrimSpace(action.Kind)) != "permission" {
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(action.Status))
	if status == "" {
		return nil
	}

	actions := renderFeishuPermissionCardActions(msg, action)
	if status == "pending" && len(actions) == 0 {
		return nil
	}

	elements := []any{
		map[string]any{
			"tag":     "markdown",
			"content": renderFeishuPermissionCardMarkdown(action),
		},
	}
	if status == "pending" {
		elements = append(elements, map[string]any{
			"tag":     "action",
			"layout":  feishuActionLayout(len(actions)),
			"actions": actions,
		})
	}

	return map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": feishuPermissionCardTemplate(status),
			"title": map[string]any{
				"tag":     "plain_text",
				"content": feishuPermissionCardTitle(status),
			},
		},
		"elements": elements,
	}
}

func renderFeishuPermissionCardActions(msg feishuActivityMessage, action feishuActivityAction) []map[string]any {
	activityID := strings.TrimSpace(action.ID)
	if activityID == "" || len(action.Options) == 0 {
		return nil
	}

	actions := make([]map[string]any, 0, len(action.Options))
	for _, option := range action.Options {
		optionID := strings.TrimSpace(option.ID)
		if optionID == "" {
			continue
		}
		buttonType := "default"
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(option.Kind)), "allow") {
			buttonType = "primary"
		}
		if strings.EqualFold(strings.TrimSpace(option.Kind), "reject") || strings.EqualFold(optionID, "reject") {
			buttonType = "danger"
		}
		actions = append(actions, map[string]any{
			"tag": "button",
			"text": map[string]any{
				"tag":     "plain_text",
				"content": firstNonEmpty(option.Label, optionID),
			},
			"type": buttonType,
			"value": map[string]any{
				"type":        "csgclaw.permission_decision",
				"channel":     firstNonEmpty(msg.Channel, "csgclaw"),
				"activity_id": activityID,
				"option_id":   optionID,
			},
		})
	}
	return actions
}

func feishuPermissionCardTemplate(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "allowed":
		return "green"
	case "rejected":
		return "red"
	case "expired", "canceled", "cancelled":
		return "grey"
	default:
		return "orange"
	}
}

func feishuPermissionCardTitle(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "allowed":
		return "Permission allowed"
	case "rejected":
		return "Permission rejected"
	case "expired":
		return "Permission expired"
	case "canceled", "cancelled":
		return "Permission canceled"
	default:
		return "Permission request"
	}
}

func renderFeishuPermissionCardMarkdown(action feishuActivityAction) string {
	var b strings.Builder
	b.WriteString("**")
	b.WriteString(escapeFeishuMarkdown(compactFeishuPermissionTitle(firstNonEmpty(action.Title, "Run tool"))))
	b.WriteString("**")
	if decision := action.Decision; decision != nil {
		b.WriteString("\nDecision: ")
		b.WriteString(escapeFeishuMarkdown(firstNonEmpty(decision.Kind, decision.OptionID)))
	}
	if expiresAt := strings.TrimSpace(action.ExpiresAt); expiresAt != "" {
		b.WriteString("\nExpires at: ")
		b.WriteString(escapeFeishuMarkdown(expiresAt))
	}
	return b.String()
}

func feishuActionLayout(count int) string {
	switch count {
	case 1:
		return "flow"
	case 2:
		return "bisected"
	default:
		return "trisection"
	}
}

func escapeFeishuMarkdown(value string) string {
	return strings.ReplaceAll(value, "`", "'")
}

func compactFeishuPermissionTitle(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return "Run tool"
	}
	return truncateFeishuText(value, 160)
}

func truncateFeishuText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit])) + "..."
}

func feishuToolStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "success", "succeeded":
		return "✅ Tool completed"
	case "failed", "failure", "error":
		return "❌ Tool failed"
	case "canceled", "cancelled":
		return "⏹️ Tool canceled"
	default:
		return "🔧 Tool call"
	}
}

func feishuSummaryValue(summary string, keys ...string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}

	var decoded any
	if err := json.Unmarshal([]byte(summary), &decoded); err != nil {
		return summary
	}
	if object, ok := decoded.(map[string]any); ok {
		for _, key := range keys {
			if value := feishuSummaryText(object[key]); value != "" {
				return value
			}
		}
		if len(keys) > 0 {
			return ""
		}
	}
	return feishuSummaryText(decoded)
}

func feishuSummaryText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64, bool:
		return strings.TrimSpace(fmt.Sprint(typed))
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
}

func appendFeishuCodeSection(b *strings.Builder, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString("\n\n")
	b.WriteString(label)
	b.WriteString("\n```\n")
	b.WriteString(strings.ReplaceAll(value, "```", "` ` `"))
	if !strings.HasSuffix(value, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("```")
}

func (t feishuActivityTool) empty() bool {
	return firstNonEmpty(t.Kind, t.Title, t.Status, t.InputSummary, t.OutputSummary) == ""
}
