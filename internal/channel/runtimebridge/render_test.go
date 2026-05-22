package runtimebridge

import (
	"encoding/json"
	"testing"
	"time"

	runtimeactivity "csgclaw/internal/runtime/activity"
)

func TestTurnRendererMergesToolUpdateDeltas(t *testing.T) {
	t.Parallel()

	renderer := NewTurnRenderer()
	now := time.Now().UTC()
	start := runtimeactivity.Event{
		RuntimeID:        "rt-1",
		SessionID:        "sess-1",
		Kind:             runtimeactivity.EventToolCallStart,
		ReceivedAt:       now,
		ToolCallID:       "tool-1",
		ToolKind:         "execute",
		ToolTitle:        "Run shell command",
		ToolStatus:       "in_progress",
		ToolInputSummary: `{"cmd":"go test ./..."}`,
	}
	if _, ok := renderer.RenderActivity(start, "room-1", "u-runtime"); !ok {
		t.Fatal("tool start was not rendered")
	}

	update := runtimeactivity.Event{
		RuntimeID:         "rt-1",
		SessionID:         "sess-1",
		Kind:              runtimeactivity.EventToolCallUpdate,
		ReceivedAt:        now.Add(time.Second),
		ToolCallID:        "tool-1",
		ToolOutputSummary: `{"output":"ok"}`,
	}
	rendered, ok := renderer.RenderActivity(update, "room-1", "u-runtime")
	if !ok {
		t.Fatal("tool output delta was not rendered")
	}

	var payload struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		Content struct {
			Tool struct {
				Title         string `json:"title"`
				Status        string `json:"status"`
				InputSummary  string `json:"input_summary"`
				OutputSummary string `json:"output_summary"`
			} `json:"tool"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(rendered.Text), &payload); err != nil {
		t.Fatalf("decode rendered activity: %v", err)
	}
	if payload.Type != AgentActivityType {
		t.Fatalf("type = %q, want %q", payload.Type, AgentActivityType)
	}
	if payload.Version != AgentActivityVersion {
		t.Fatalf("version = %d, want %d", payload.Version, AgentActivityVersion)
	}
	if payload.Content.Tool.Title != "Run shell command" {
		t.Fatalf("title = %q, want prior title", payload.Content.Tool.Title)
	}
	if payload.Content.Tool.Status != "running" {
		t.Fatalf("status = %q, want running", payload.Content.Tool.Status)
	}
	if payload.Content.Tool.InputSummary == "" || payload.Content.Tool.OutputSummary == "" {
		t.Fatalf("tool summaries = input %q output %q, want both retained", payload.Content.Tool.InputSummary, payload.Content.Tool.OutputSummary)
	}

	completed := runtimeactivity.Event{
		RuntimeID:  "rt-1",
		SessionID:  "sess-1",
		Kind:       runtimeactivity.EventToolCallUpdate,
		ReceivedAt: now.Add(2 * time.Second),
		ToolCallID: "tool-1",
		ToolStatus: "completed",
	}
	if _, ok := renderer.RenderActivity(completed, "room-1", "u-runtime"); !ok {
		t.Fatal("tool completed delta was not rendered")
	}

	laterOutput := runtimeactivity.Event{
		RuntimeID:         "rt-1",
		SessionID:         "sess-1",
		Kind:              runtimeactivity.EventToolCallUpdate,
		ReceivedAt:        now.Add(3 * time.Second),
		ToolCallID:        "tool-1",
		ToolOutputSummary: `{"output":"done"}`,
	}
	rendered, ok = renderer.RenderActivity(laterOutput, "room-1", "u-runtime")
	if !ok {
		t.Fatal("post-completion output delta was not rendered")
	}
	if err := json.Unmarshal([]byte(rendered.Text), &payload); err != nil {
		t.Fatalf("decode post-completion activity: %v", err)
	}
	if payload.Content.Tool.Status != "completed" {
		t.Fatalf("post-completion status = %q, want completed", payload.Content.Tool.Status)
	}
}
