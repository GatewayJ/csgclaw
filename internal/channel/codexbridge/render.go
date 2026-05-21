package codexbridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	runtimecodex "csgclaw/internal/runtime/codex"
)

const (
	agentActivityType        = "com.opencsg.csgclaw.agent.activity"
	agentToolMsgType         = "com.opencsg.csgclaw.agent.tool"
	agentPermissionMsgType   = "com.opencsg.csgclaw.agent.permission"
	agentActivityRuntimeKind = "codex"
)

type turnRenderer struct {
	text        strings.Builder
	toolStates  map[string]string
	promptError string
}

func newTurnRenderer() *turnRenderer {
	return &turnRenderer{
		toolStates: make(map[string]string),
	}
}

func (r *turnRenderer) Apply(event runtimecodex.SessionEvent) []string {
	if r == nil {
		return nil
	}

	switch event.Kind {
	case runtimecodex.SessionEventTextDelta:
		if event.Text != "" {
			_, _ = r.text.WriteString(event.Text)
		}
	case runtimecodex.SessionEventPromptFailed:
		r.promptError = strings.TrimSpace(event.Error)
	}
	return nil
}

func (r *turnRenderer) FinalMessages() []string {
	if r == nil {
		return nil
	}
	var messages []string
	if text := strings.TrimSpace(r.text.String()); text != "" {
		messages = append(messages, text)
	}
	if r.promptError != "" {
		messages = append(messages, fmt.Sprintf("Codex runtime error: %s", r.promptError))
	}
	return messages
}

func (r *turnRenderer) ShouldRenderActivity(event runtimecodex.SessionEvent) bool {
	if r == nil {
		return false
	}
	switch event.Kind {
	case runtimecodex.SessionEventToolCallStart:
		if event.ToolCallID != "" {
			r.toolStates[event.ToolCallID] = normalizedToolStatus(event.ToolStatus)
		}
		return true
	case runtimecodex.SessionEventToolCallUpdate:
		status := normalizedToolStatus(event.ToolStatus)
		if event.ToolCallID != "" && r.toolStates[event.ToolCallID] == status {
			return false
		}
		if event.ToolCallID != "" {
			r.toolStates[event.ToolCallID] = status
		}
		return true
	case runtimecodex.SessionEventPermissionRequest, runtimecodex.SessionEventPermissionDecision:
		return true
	default:
		return false
	}
}

func displayToolTitle(event runtimecodex.SessionEvent) string {
	title := strings.TrimSpace(event.ToolTitle)
	if title == "" {
		title = "Run tool"
	}
	return title
}

func normalizedToolStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "in_progress":
		return "running"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

type renderedActivity struct {
	MessageID string
	Text      string
}

func renderActivity(event runtimecodex.SessionEvent, binding Binding, roomID, senderID string) (renderedActivity, bool) {
	var content any
	switch event.Kind {
	case runtimecodex.SessionEventToolCallStart, runtimecodex.SessionEventToolCallUpdate:
		content = toolActivityContent(event)
	case runtimecodex.SessionEventPermissionRequest, runtimecodex.SessionEventPermissionDecision:
		snapshot, ok := event.Payload.(runtimecodex.PermissionSnapshot)
		if !ok {
			return renderedActivity{}, false
		}
		content = permissionActivityContent(event, snapshot)
	default:
		return renderedActivity{}, false
	}

	eventID := activityEventID(event)
	payload := agentActivityPayload{
		Type:           agentActivityType,
		EventID:        eventID,
		RoomID:         strings.TrimSpace(roomID),
		Sender:         strings.TrimSpace(senderID),
		OriginServerTS: event.ReceivedAt.UnixMilli(),
		Content:        content,
	}
	if payload.OriginServerTS == 0 {
		payload.OriginServerTS = time.Now().UTC().UnixMilli()
	}
	if payload.Sender == "" {
		payload.Sender = strings.TrimSpace(binding.BotID)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return renderedActivity{}, false
	}
	return renderedActivity{MessageID: eventID, Text: string(data)}, true
}

type agentActivityPayload struct {
	Type           string `json:"type"`
	EventID        string `json:"event_id"`
	RoomID         string `json:"room_id"`
	Sender         string `json:"sender"`
	OriginServerTS int64  `json:"origin_server_ts"`
	Content        any    `json:"content"`
}

type activityRuntime struct {
	Kind      string `json:"kind"`
	RuntimeID string `json:"runtime_id"`
	SessionID string `json:"session_id"`
}

type toolActivity struct {
	MsgType string          `json:"msgtype"`
	Body    string          `json:"body"`
	Runtime activityRuntime `json:"runtime"`
	Tool    activityTool    `json:"tool"`
}

type activityTool struct {
	ID            string `json:"id"`
	Kind          string `json:"kind,omitempty"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	InputSummary  string `json:"input_summary,omitempty"`
	OutputSummary string `json:"output_summary,omitempty"`
}

type permissionActivity struct {
	MsgType    string             `json:"msgtype"`
	Body       string             `json:"body"`
	Runtime    activityRuntime    `json:"runtime"`
	Permission activityPermission `json:"permission"`
}

type activityPermission struct {
	ID          string                                   `json:"id"`
	ToolCallID  string                                   `json:"tool_call_id"`
	Title       string                                   `json:"title"`
	Status      string                                   `json:"status"`
	RequestedAt string                                   `json:"requested_at,omitempty"`
	ExpiresAt   string                                   `json:"expires_at,omitempty"`
	Options     []runtimecodex.PermissionOptionSnapshot  `json:"options,omitempty"`
	Decision    *runtimecodex.PermissionDecisionSnapshot `json:"decision,omitempty"`
}

func toolActivityContent(event runtimecodex.SessionEvent) toolActivity {
	status := normalizedToolStatus(event.ToolStatus)
	return toolActivity{
		MsgType: agentToolMsgType,
		Body:    fmt.Sprintf("Tool %s: %s", status, displayToolTitle(event)),
		Runtime: activityRuntime{
			Kind:      agentActivityRuntimeKind,
			RuntimeID: strings.TrimSpace(event.RuntimeID),
			SessionID: strings.TrimSpace(event.SessionID),
		},
		Tool: activityTool{
			ID:            strings.TrimSpace(event.ToolCallID),
			Kind:          strings.TrimSpace(event.ToolKind),
			Title:         displayToolTitle(event),
			Status:        status,
			InputSummary:  strings.TrimSpace(event.ToolInputSummary),
			OutputSummary: strings.TrimSpace(event.ToolOutputSummary),
		},
	}
}

func permissionActivityContent(event runtimecodex.SessionEvent, snapshot runtimecodex.PermissionSnapshot) permissionActivity {
	status := string(snapshot.Status)
	bodyStatus := "Codex wants permission"
	switch snapshot.Status {
	case runtimecodex.PermissionStatusAllowed:
		bodyStatus = "Permission allowed"
	case runtimecodex.PermissionStatusRejected:
		bodyStatus = "Permission rejected"
	case runtimecodex.PermissionStatusExpired:
		bodyStatus = "Permission expired"
	case runtimecodex.PermissionStatusCanceled:
		bodyStatus = "Permission canceled"
	}
	return permissionActivity{
		MsgType: agentPermissionMsgType,
		Body:    fmt.Sprintf("%s: %s", bodyStatus, displayToolTitle(event)),
		Runtime: activityRuntime{
			Kind:      agentActivityRuntimeKind,
			RuntimeID: strings.TrimSpace(snapshot.RuntimeID),
			SessionID: strings.TrimSpace(snapshot.SessionID),
		},
		Permission: activityPermission{
			ID:          strings.TrimSpace(snapshot.ID),
			ToolCallID:  strings.TrimSpace(snapshot.ToolCallID),
			Title:       firstActivityText(snapshot.ToolTitle, displayToolTitle(event)),
			Status:      status,
			RequestedAt: formatActivityTime(snapshot.RequestedAt),
			ExpiresAt:   formatActivityTime(snapshot.ExpiresAt),
			Options:     snapshot.Options,
			Decision:    snapshot.Decision,
		},
	}
}

func activityEventID(event runtimecodex.SessionEvent) string {
	parts := []string{"act", strings.TrimSpace(event.RuntimeID), strings.TrimSpace(event.SessionID)}
	if event.PermissionRequestID != "" {
		parts = append(parts, event.PermissionRequestID)
		return joinActivityIDParts(parts)
	} else if event.ToolCallID != "" {
		parts = append(parts, event.ToolCallID)
	}
	if event.PermissionStatus != "" {
		parts = append(parts, event.PermissionStatus)
	} else if event.ToolStatus != "" {
		parts = append(parts, normalizedToolStatus(event.ToolStatus))
	}
	if !event.ReceivedAt.IsZero() {
		parts = append(parts, fmt.Sprintf("%d", event.ReceivedAt.UnixNano()))
	}
	return joinActivityIDParts(parts)
}

func joinActivityIDParts(parts []string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "-")
}

func formatActivityTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func firstActivityText(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
