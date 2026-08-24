package presentation

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"csgclaw/internal/agentengine"
)

func TestRunCardUsesLocalToolPanelsAndKeepsThemAtTerminal(t *testing.T) {
	progress := NewProgress(ModeCard, "turn-1", "conversation-1", "thread-1")
	initial := progress.render()
	assertCardConfig(t, initial.Card, true)
	assertCardV2RunningControls(t, initial.Card)

	startCard, flush := progress.Observe(agentengine.TurnEvent{
		Kind: agentengine.TurnEventToolCallStart,
		Tool: &agentengine.ToolActivity{
			ID: "tool-1", Kind: "exec_command", Title: "Run shell command", Status: "started",
			InputSummary: `{"command":"go test ./..."}`,
			Payload:      map[string]any{"runtime_id": "must-not-leak"},
		},
	})
	if !flush {
		t.Fatal("tool start did not flush")
	}
	startJSON := cardJSON(t, startCard.Card)
	for _, want := range []string{"collapsible_panel", "⏳", "Bash", "go test ./...", "运行中", "正在输出…"} {
		if !strings.Contains(startJSON, want) {
			t.Fatalf("running tool card missing %q: %s", want, startJSON)
		}
	}
	if strings.Contains(startJSON, "must-not-leak") {
		t.Fatalf("running tool card leaked Engine payload: %s", startJSON)
	}

	doneCard, flush := progress.Observe(agentengine.TurnEvent{
		Kind: agentengine.TurnEventToolCallUpdate,
		Tool: &agentengine.ToolActivity{
			ID: "tool-1", Kind: "exec_command", Status: "completed", OutputSummary: `{"output":"ok"}`,
		},
	})
	if !flush {
		t.Fatal("tool result did not flush")
	}
	doneJSON := cardJSON(t, doneCard.Card)
	if !strings.Contains(doneJSON, "✅") || !strings.Contains(doneJSON, `\"output\":\"ok\"`) {
		t.Fatalf("completed tool panel = %s", doneJSON)
	}

	_, _ = progress.Observe(agentengine.TurnEvent{Kind: agentengine.TurnEventTextDelta, Text: "final answer"})
	terminal := progress.Finalize(agentengine.TurnResult{Status: agentengine.TurnSucceeded, Output: "final answer"})
	assertCardConfig(t, terminal.Card, false)
	terminalJSON := cardJSON(t, terminal.Card)
	for _, want := range []string{"collapsible_panel", "✅", "Bash", "final answer"} {
		if !strings.Contains(terminalJSON, want) {
			t.Fatalf("terminal card missing %q: %s", want, terminalJSON)
		}
	}
	if strings.Contains(terminalJSON, `"tag":"button"`) {
		t.Fatalf("terminal card retained Stop action: %s", terminalJSON)
	}
	if strings.Contains(terminalJSON, "正在思考…") || strings.Contains(terminalJSON, "正在输出…") {
		t.Fatalf("terminal card retained running status: %s", terminalJSON)
	}
}

func TestRunCardCollapsesThreeCompletedTools(t *testing.T) {
	progress := NewProgress(ModeCard, "turn-1", "conversation-1", "")
	for index := 1; index <= 3; index++ {
		id := fmt.Sprintf("tool-%d", index)
		_, _ = progress.Observe(agentengine.TurnEvent{
			Kind: agentengine.TurnEventToolCallStart,
			Tool: &agentengine.ToolActivity{ID: id, Kind: "mcp_tool_call", Title: "MCP tool", Status: "started"},
		})
		_, _ = progress.Observe(agentengine.TurnEvent{
			Kind: agentengine.TurnEventToolCallUpdate,
			Tool: &agentengine.ToolActivity{ID: id, Status: "completed", OutputSummary: "ok"},
		})
	}
	card := progress.Finalize(agentengine.TurnResult{Status: agentengine.TurnSucceeded})
	encoded := cardJSON(t, card.Card)
	if !strings.Contains(encoded, "3 个工具调用（已结束）") || !strings.Contains(encoded, "collapsible_panel") {
		t.Fatalf("collapsed tool summary = %s", encoded)
	}
}

func TestRunPresentationCoalescesDeltasAndBoundsEscapedWirePayload(t *testing.T) {
	progress := NewProgress(ModeCard, "turn-1", "conversation-1", "")
	_, first := progress.Observe(agentengine.TurnEvent{Kind: agentengine.TurnEventTextDelta, Text: strings.Repeat("a", 4096)})
	_, second := progress.Observe(agentengine.TurnEvent{Kind: agentengine.TurnEventTextDelta, Text: strings.Repeat("b", 4096)})
	if !first || second {
		t.Fatalf("delta flushes first=%t second=%t", first, second)
	}
	rendered, _ := progress.Observe(agentengine.TurnEvent{
		Kind: agentengine.TurnEventTextDelta,
		Text: strings.Repeat("\x00\\\"\n", 20_000),
	})
	if rendered.Card == nil {
		rendered = progress.render()
	}
	if size := cardWireBytes(rendered.Card); size > maxRichWireBytes {
		t.Fatalf("card wire size = %d, want <= %d", size, maxRichWireBytes)
	}
}

func TestRunPresentationSplitsOversizedMarkdownWithoutDroppingContent(t *testing.T) {
	progress := NewProgress(ModeMarkdown, "turn-1", "conversation-1", "")
	rendered, flush := progress.Observe(agentengine.TurnEvent{
		Kind: agentengine.TurnEventTextDelta,
		Text: strings.Repeat("\x00", 8_000),
	})
	if !flush || len(rendered.MarkdownParts) < 2 {
		t.Fatalf("markdown parts = %d, flush=%t, want multiple parts", len(rendered.MarkdownParts), flush)
	}
	if rendered.Markdown != rendered.MarkdownParts[0] {
		t.Fatalf("first markdown part = %q, want %q", rendered.Markdown, rendered.MarkdownParts[0])
	}
	want := progress.renderMarkdown()
	if got := strings.Join(rendered.MarkdownParts, ""); got != want {
		t.Fatalf("joined markdown parts lost content: got %d bytes, want %d", len(got), len(want))
	}
	for index, part := range rendered.MarkdownParts {
		if size := markdownWireBytes(part); size > maxRichWireBytes {
			t.Fatalf("markdown part %d wire size = %d, want <= %d", index, size, maxRichWireBytes)
		}
	}
}

func TestRunMarkdownUsesLocalToolPresentation(t *testing.T) {
	progress := NewProgress(ModeMarkdown, "turn-1", "conversation-1", "")
	initial := progress.render()
	if initial.Mode != ModeMarkdown || initial.Card != nil || !strings.Contains(initial.Markdown, "正在思考") {
		t.Fatalf("initial markdown = %#v", initial)
	}

	running, flush := progress.Observe(agentengine.TurnEvent{
		Kind: agentengine.TurnEventToolCallStart,
		Tool: &agentengine.ToolActivity{
			ID: "tool-1", Kind: "exec_command", Status: "started",
			InputSummary: `{"command":"git status --short"}`,
		},
	})
	if !flush || !strings.Contains(running.Markdown, "> ⏳ **command_execution** — git status --short") {
		t.Fatalf("running markdown = %q", running.Markdown)
	}

	done, flush := progress.Observe(agentengine.TurnEvent{
		Kind: agentengine.TurnEventToolCallUpdate,
		Tool: &agentengine.ToolActivity{
			ID: "tool-1", Kind: "exec_command", Status: "completed",
			OutputSummary: `{"output":"must not be rendered"}`,
		},
	})
	if !flush || !strings.Contains(done.Markdown, "> ✅ **command_execution** — git status --short") {
		t.Fatalf("completed markdown = %q", done.Markdown)
	}
	if strings.Contains(done.Markdown, "must not be rendered") {
		t.Fatalf("completed markdown exposes tool output: %q", done.Markdown)
	}

	failed, flush := progress.Observe(agentengine.TurnEvent{
		Kind: agentengine.TurnEventToolCallStart,
		Tool: &agentengine.ToolActivity{
			ID: "tool-2", Kind: "exec_command", Status: "started",
			InputSummary: `{"command":"rg -n TODO ."}`,
		},
	})
	if !flush {
		t.Fatal("second tool start did not flush")
	}
	failed, flush = progress.Observe(agentengine.TurnEvent{
		Kind: agentengine.TurnEventToolCallUpdate,
		Tool: &agentengine.ToolActivity{
			ID: "tool-2", Kind: "exec_command", Status: "failed",
			OutputSummary: `{"output":"also hidden"}`,
		},
	})
	if !flush {
		t.Fatal("second tool result did not flush")
	}
	wantTools := "> ✅ **command_execution** — git status --short\n> ❌ **command_execution** — rg -n TODO ."
	if !strings.Contains(failed.Markdown, wantTools) {
		t.Fatalf("compact tool lines missing from markdown: %q", failed.Markdown)
	}
	if strings.Contains(failed.Markdown, "also hidden") {
		t.Fatalf("failed markdown exposes tool output: %q", failed.Markdown)
	}
	_, _ = progress.Observe(agentengine.TurnEvent{Kind: agentengine.TurnEventTextDelta, Text: "final answer"})
	terminal := progress.Finalize(agentengine.TurnResult{Status: agentengine.TurnSucceeded, Output: "final answer"})
	if terminal.Card != nil || !strings.Contains(terminal.Markdown, "final answer") || strings.Contains(terminal.Markdown, "正在输出") {
		t.Fatalf("terminal markdown = %#v", terminal)
	}
}

func TestRunMarkdownRecoversAndCompactsTruncatedCommandSummary(t *testing.T) {
	progress := NewProgress(ModeMarkdown, "turn-1", "conversation-1", "")
	rendered, flush := progress.Observe(agentengine.TurnEvent{
		Kind: agentengine.TurnEventToolCallStart,
		Tool: &agentengine.ToolActivity{
			ID: "tool-1", Kind: "exec_command", Status: "started",
			InputSummary: `{"command":"printf first\nsecond \u0026\u0026 ` + strings.Repeat("x", 160) + "...",
		},
	})
	if !flush {
		t.Fatal("truncated command start did not flush")
	}
	line := strings.SplitN(rendered.Markdown, "\n", 2)[0]
	const prefix = "> ⏳ **command_execution** — "
	if !strings.HasPrefix(line, prefix+"printf first second && ") {
		t.Fatalf("truncated command line = %q", line)
	}
	if strings.Contains(line, `{"command"`) || strings.Contains(line, `\u0026`) {
		t.Fatalf("truncated command leaked serialized JSON: %q", line)
	}
	summary := strings.TrimPrefix(line, prefix)
	if got := utf8.RuneCountInString(summary); got != maxToolSummaryRunes+1 || !strings.HasSuffix(summary, "…") {
		t.Fatalf("compacted summary = %q (%d runes)", summary, got)
	}
}

func TestNormalizeModeDefaultsToMarkdown(t *testing.T) {
	for _, value := range []string{"", "unknown", " markdown "} {
		if got := NormalizeMode(value); got != ModeMarkdown {
			t.Fatalf("NormalizeMode(%q) = %q", value, got)
		}
	}
	if got := NormalizeMode(" CARD "); got != ModeCard {
		t.Fatalf("NormalizeMode(card) = %q", got)
	}
}

func assertCardConfig(t *testing.T, card map[string]any, streaming bool) {
	t.Helper()
	config, ok := card["config"].(map[string]any)
	if !ok || config["streaming_mode"] != streaming || config["update_multi"] != true {
		t.Fatalf("card config = %#v", card["config"])
	}
}

func assertCardV2RunningControls(t *testing.T, card map[string]any) {
	t.Helper()
	body, ok := card["body"].(map[string]any)
	if !ok {
		t.Fatalf("card body = %#v", card["body"])
	}
	elements, ok := body["elements"].([]any)
	if !ok || len(elements) != 2 {
		t.Fatalf("initial card elements = %#v, want status and Card 2.0 button", body["elements"])
	}
	status, ok := elements[0].(map[string]any)
	if !ok || status["tag"] != "markdown" || status["content"] != "🧠 正在思考…" || status["text_size"] != "notation" {
		t.Fatalf("initial Card 2.0 running status = %#v", elements[0])
	}
	button, ok := elements[1].(map[string]any)
	if !ok || button["tag"] != "button" || button["type"] != "danger" {
		t.Fatalf("initial Card 2.0 button = %#v", elements[1])
	}
	if _, legacy := button["value"]; legacy {
		t.Fatalf("initial Card 2.0 button has legacy top-level value: %#v", button)
	}
	text, ok := button["text"].(map[string]any)
	if !ok || text["tag"] != "plain_text" || text["content"] != "⏹ 终止" {
		t.Fatalf("initial Card 2.0 button text = %#v", button["text"])
	}
	behaviors, ok := button["behaviors"].([]any)
	if !ok || len(behaviors) != 1 {
		t.Fatalf("initial Card 2.0 button behaviors = %#v", button["behaviors"])
	}
	callback, ok := behaviors[0].(map[string]any)
	if !ok || callback["type"] != "callback" {
		t.Fatalf("initial Card 2.0 callback = %#v", behaviors[0])
	}
	value, ok := callback["value"].(map[string]any)
	if !ok || value["cmd"] != "stop" {
		t.Fatalf("initial Card 2.0 callback value = %#v", callback["value"])
	}
}

func cardJSON(t *testing.T, card map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
