package channelbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"csgclaw/internal/channel/feishu"
	"csgclaw/internal/channelbridge/runtimebridge"
)

func TestFeishuClientFormatsToolActivityMessages(t *testing.T) {
	t.Parallel()

	var gotReq feishu.SendMessageRequest
	svc := feishu.NewServiceWithSendMessage(
		map[string]feishu.AppConfig{
			"manager": {AppID: "cli_manager", AppSecret: "manager-secret"},
		},
		func(_ context.Context, _ feishu.AppConfig, req feishu.SendMessageRequest) (feishu.SendMessageResponse, error) {
			gotReq = req
			return feishu.SendMessageResponse{MessageID: "om_tool", SenderOpenID: "ou_codex"}, nil
		},
	)
	client := NewFeishuClient(svc)

	activityData, err := json.Marshal(map[string]any{
		"type":             runtimebridge.AgentActivityType,
		"version":          1,
		"channel":          "csgclaw",
		"event_id":         "tool-f8e39e393ff08ef6dd33f95b",
		"room_id":          "oc_afb50868a26e1732e5ad4ad20a0d9391",
		"sender":           "agent-2te2nl",
		"origin_server_ts": int64(1781792759561),
		"content": map[string]any{
			"msgtype": runtimebridge.AgentToolMsgType,
			"body":    "Tool completed: Run shell command",
			"tool": map[string]any{
				"id":             "f8e39e393ff08ef6dd33f95b",
				"kind":           "exec_command",
				"title":          "Run shell command",
				"status":         "completed",
				"input_summary":  `{"command":"/bin/bash -lc 'which csgclaw-cli || find /home/jhw -name csgclaw-cli -type f 2>/dev/null | head'"}`,
				"output_summary": `{"output":"/home/jhw/opcsg/csgclaw/bin/csgclaw-cli"}`,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal activity: %v", err)
	}

	resp, err := client.SendMessage(context.Background(), "u-codex", SendMessageRequest{
		RoomID:       "oc_afb50868a26e1732e5ad4ad20a0d9391",
		Text:         string(activityData),
		ThreadRootID: "om_root",
	})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if resp.MessageID != "om_tool" {
		t.Fatalf("MessageID = %q, want om_tool", resp.MessageID)
	}
	if gotReq.ThreadRootID != "om_root" {
		t.Fatalf("ThreadRootID = %q, want om_root", gotReq.ThreadRootID)
	}

	content := gotReq.Content
	for _, want := range []string{
		"✅ Tool completed · Run shell command",
		"Command\n```\n/bin/bash -lc 'which csgclaw-cli || find /home/jhw -name csgclaw-cli -type f 2>/dev/null | head'\n```",
		"Result\n```\n/home/jhw/opcsg/csgclaw/bin/csgclaw-cli\n```",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("formatted content missing %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{
		"event_id",
		"room_id",
		"sender",
		"origin_server_ts",
		"input_summary",
		"output_summary",
		runtimebridge.AgentActivityType,
		runtimebridge.AgentToolMsgType,
	} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("formatted content leaked %q:\n%s", unwanted, content)
		}
	}
}
