package channelbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"csgclaw/internal/activity"
	"csgclaw/internal/channel/feishu"
	"csgclaw/internal/channelbridge/runtimebridge"

	larkcallback "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
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

func TestFeishuClientSendsPermissionActivityAsInteractiveCard(t *testing.T) {
	t.Parallel()

	var gotReq feishu.SendInteractiveMessageRequest
	svc := feishu.NewServiceWithInteractiveMessage(
		map[string]feishu.AppConfig{
			"u-codex": {AppID: "cli_codex", AppSecret: "codex-secret"},
		},
		func(_ context.Context, _ feishu.AppConfig, req feishu.SendInteractiveMessageRequest) (feishu.SendMessageResponse, error) {
			gotReq = req
			return feishu.SendMessageResponse{MessageID: "om_permission", SenderOpenID: "ou_codex"}, nil
		},
	)
	client := NewFeishuClient(svc)

	activityData, err := json.Marshal(map[string]any{
		"type":             runtimebridge.AgentActivityType,
		"version":          1,
		"channel":          "csgclaw",
		"event_id":         "act-perm-1",
		"room_id":          "oc_room",
		"sender":           "agent-1",
		"origin_server_ts": int64(1781792759561),
		"content": map[string]any{
			"msgtype": runtimebridge.AgentActionMsgType,
			"body":    "Permission required: Run shell command: go test ./...",
			"action": map[string]any{
				"id":     "perm-1",
				"kind":   "permission",
				"title":  "Run shell command: go test ./...",
				"status": "pending",
				"options": []map[string]any{
					{"id": "allow_once", "kind": "allow_once", "label": "Allow once"},
					{"id": "reject", "kind": "reject", "label": "Reject"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal activity: %v", err)
	}

	resp, err := client.SendMessage(context.Background(), "u-codex", SendMessageRequest{
		RoomID:       "oc_room",
		Text:         string(activityData),
		ThreadRootID: "om_root",
	})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if resp.MessageID != "om_permission" {
		t.Fatalf("MessageID = %q, want om_permission", resp.MessageID)
	}
	if gotReq.SenderID != "u-codex" || gotReq.ChatID != "oc_room" || gotReq.ThreadRootID != "om_root" {
		t.Fatalf("interactive request = %+v, want sender/chat/thread", gotReq)
	}

	var card map[string]any
	if err := json.Unmarshal([]byte(gotReq.Content), &card); err != nil {
		t.Fatalf("card content is not JSON: %v\n%s", err, gotReq.Content)
	}
	cardData, _ := json.Marshal(card)
	cardText := string(cardData)
	for _, want := range []string{
		"Permission request",
		"Run shell command: go test ./...",
		"allow_once",
		"reject",
		"csgclaw.permission_decision",
		"perm-1",
	} {
		if !strings.Contains(cardText, want) {
			t.Fatalf("card missing %q:\n%s", want, cardText)
		}
	}
	if strings.Contains(cardText, "Permission required: Run shell command") {
		t.Fatalf("pending card leaked duplicated body:\n%s", cardText)
	}
}

func TestFeishuClientUpdatesPermissionActivityAsTerminalCard(t *testing.T) {
	t.Parallel()

	var gotReq feishu.UpdateMessageRequest
	svc := feishu.NewServiceWithUpdateMessage(
		map[string]feishu.AppConfig{
			"u-codex": {AppID: "cli_codex", AppSecret: "codex-secret"},
		},
		func(_ context.Context, _ feishu.AppConfig, req feishu.UpdateMessageRequest) (feishu.UpdateMessageResponse, error) {
			gotReq = req
			return feishu.UpdateMessageResponse{MessageID: req.MessageID}, nil
		},
	)
	client := NewFeishuClient(svc)

	activityData, err := json.Marshal(map[string]any{
		"type":             runtimebridge.AgentActivityType,
		"version":          1,
		"channel":          "csgclaw",
		"event_id":         "act-perm-1",
		"room_id":          "oc_room",
		"sender":           "agent-1",
		"origin_server_ts": int64(1781792759561),
		"content": map[string]any{
			"msgtype": runtimebridge.AgentActionMsgType,
			"body":    "Permission allowed: Run shell command: go test ./...",
			"action": map[string]any{
				"id":     "perm-1",
				"kind":   "permission",
				"title":  "Run shell command: go test ./...",
				"status": "allowed",
				"decision": map[string]any{
					"option_id": "allow_once",
					"kind":      "allow_once",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal activity: %v", err)
	}

	resp, err := client.UpdateMessage(context.Background(), "u-codex", UpdateMessageRequest{
		RoomID:    "oc_room",
		MessageID: "om_permission",
		Text:      string(activityData),
	})
	if err != nil {
		t.Fatalf("UpdateMessage() error = %v", err)
	}
	if resp.MessageID != "om_permission" {
		t.Fatalf("MessageID = %q, want om_permission", resp.MessageID)
	}
	if gotReq.SenderID != "u-codex" || gotReq.RoomID != "oc_room" || gotReq.MessageID != "om_permission" || gotReq.MessageType != "interactive" {
		t.Fatalf("update request = %+v, want interactive update", gotReq)
	}

	var card map[string]any
	if err := json.Unmarshal([]byte(gotReq.Content), &card); err != nil {
		t.Fatalf("card content is not JSON: %v\n%s", err, gotReq.Content)
	}
	cardData, _ := json.Marshal(card)
	cardText := string(cardData)
	for _, want := range []string{
		"Permission allowed",
		"Run shell command: go test ./...",
		"allow_once",
	} {
		if !strings.Contains(cardText, want) {
			t.Fatalf("card missing %q:\n%s", want, cardText)
		}
	}
	if strings.Contains(cardText, "csgclaw.permission_decision") {
		t.Fatalf("terminal card still contains decision action:\n%s", cardText)
	}
	if strings.Contains(cardText, "Permission allowed: Run shell command") {
		t.Fatalf("terminal card leaked duplicated body:\n%s", cardText)
	}
}

func TestFeishuClientCardActionTriggerDecidesPermissionOverLongConnection(t *testing.T) {
	t.Parallel()

	decider := &recordingActivityDecider{
		snapshot: activity.ActivitySnapshot{
			ID:     "perm-1",
			Kind:   activity.ActionKindPermission,
			Title:  "Run shell command: go test ./...",
			Status: activity.ActionStatusAllowed,
			Options: []activity.ActionOptionSnapshot{
				{ID: "allow_once", Kind: "allow_once", Label: "Allow once"},
				{ID: "reject", Kind: "reject", Label: "Reject"},
			},
			Decision: &activity.ActionDecisionSnapshot{
				OptionID: "allow_once",
				Kind:     "allow_once",
			},
		},
	}
	client := &FeishuClient{ActivityDecider: decider}

	resp, err := client.handleCardActionTrigger(context.Background(), &larkcallback.CardActionTriggerEvent{
		Event: &larkcallback.CardActionTriggerRequest{
			Action: &larkcallback.CallBackAction{
				Value: map[string]interface{}{
					"type":        "csgclaw.permission_decision",
					"channel":     "csgclaw",
					"activity_id": "perm-1",
					"option_id":   "allow_once",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("handleCardActionTrigger() error = %v", err)
	}
	if decider.req.Channel != "csgclaw" || decider.req.ActivityID != "perm-1" || decider.req.OptionID != "allow_once" {
		t.Fatalf("decision request = %+v, want card value fields", decider.req)
	}
	if resp == nil || resp.Toast == nil {
		t.Fatalf("response = %#v, want toast", resp)
	}
	if resp.Toast.Type != "success" || resp.Toast.Content != "Decision submitted" {
		t.Fatalf("toast = %+v, want success decision toast", resp.Toast)
	}
	if resp.Card == nil || resp.Card.Type != "raw" {
		t.Fatalf("response card = %#v, want raw terminal card", resp.Card)
	}
	cardData, _ := json.Marshal(resp.Card.Data)
	cardText := string(cardData)
	for _, want := range []string{
		"Permission allowed",
		"Run shell command: go test ./...",
		"allow_once",
	} {
		if !strings.Contains(cardText, want) {
			t.Fatalf("callback card missing %q:\n%s", want, cardText)
		}
	}
	if strings.Contains(cardText, "csgclaw.permission_decision") {
		t.Fatalf("callback terminal card still contains decision action:\n%s", cardText)
	}
}

func TestFeishuClientCardActionTriggerMapsGonePermissionToWarningToast(t *testing.T) {
	t.Parallel()

	client := &FeishuClient{ActivityDecider: &recordingActivityDecider{err: activity.ErrActionGone}}

	resp, err := client.handleCardActionTrigger(context.Background(), &larkcallback.CardActionTriggerEvent{
		Event: &larkcallback.CardActionTriggerRequest{
			Action: &larkcallback.CallBackAction{
				Value: map[string]interface{}{
					"type":        "csgclaw.permission_decision",
					"channel":     "csgclaw",
					"activity_id": "perm-1",
					"option_id":   "allow_once",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("handleCardActionTrigger() error = %v", err)
	}
	if resp == nil || resp.Toast == nil {
		t.Fatalf("response = %#v, want toast", resp)
	}
	if resp.Toast.Type != "warning" || resp.Toast.Content != "This request is no longer pending" {
		t.Fatalf("toast = %+v, want warning gone toast", resp.Toast)
	}
}

type recordingActivityDecider struct {
	req      activity.ActivityDecisionRequest
	snapshot activity.ActivitySnapshot
	err      error
}

func (d *recordingActivityDecider) Decide(_ context.Context, req activity.ActivityDecisionRequest) (activity.ActivitySnapshot, error) {
	d.req = req
	if d.snapshot.ID == "" {
		d.snapshot.ID = req.ActivityID
	}
	return d.snapshot, d.err
}
