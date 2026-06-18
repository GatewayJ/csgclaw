package channelbridge

import (
	"encoding/json"
	"fmt"
	"strings"

	"csgclaw/internal/channelbridge/runtimebridge"
)

type feishuActivityMessage struct {
	Type    string                `json:"type"`
	Content feishuActivityContent `json:"content"`
}

type feishuActivityContent struct {
	MsgType string             `json:"msgtype"`
	Body    string             `json:"body"`
	Tool    feishuActivityTool `json:"tool"`
}

type feishuActivityTool struct {
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	InputSummary  string `json:"input_summary"`
	OutputSummary string `json:"output_summary"`
}

func formatFeishuBridgeMessageText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return text
	}

	var msg feishuActivityMessage
	if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
		return text
	}
	if msg.Type != runtimebridge.AgentActivityType {
		return text
	}
	if msg.Content.MsgType != runtimebridge.AgentToolMsgType {
		return firstNonEmpty(msg.Content.Body, text)
	}
	if msg.Content.Tool.empty() {
		return firstNonEmpty(msg.Content.Body, text)
	}
	return renderFeishuToolActivity(msg.Content.Tool, msg.Content.Body)
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
