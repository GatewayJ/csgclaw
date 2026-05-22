package runtimebridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	runtimeactivity "csgclaw/internal/runtime/activity"
)

const (
	AgentActivityVersion   = 1
	AgentActivityType      = "com.opencsg.csgclaw.agent.activity"
	AgentToolMsgType       = "com.opencsg.csgclaw.agent.tool"
	AgentPermissionMsgType = "com.opencsg.csgclaw.agent.permission"
)

type TurnRenderer struct {
	text           strings.Builder
	toolSnapshots  map[string]activityTool
	toolSignatures map[string]string
	promptError    string
}

func NewTurnRenderer() *TurnRenderer {
	return &TurnRenderer{
		toolSnapshots:  make(map[string]activityTool),
		toolSignatures: make(map[string]string),
	}
}

func (r *TurnRenderer) ApplyText(event runtimeactivity.Event) {
	if r == nil {
		return
	}

	switch event.Kind {
	case runtimeactivity.EventTextDelta:
		if event.Text != "" {
			_, _ = r.text.WriteString(event.Text)
		}
	case runtimeactivity.EventPromptFailed:
		r.promptError = strings.TrimSpace(event.Error)
	}
}

func (r *TurnRenderer) FinalMessages() []string {
	if r == nil {
		return nil
	}
	var messages []string
	if text := strings.TrimSpace(r.text.String()); text != "" {
		messages = append(messages, text)
	}
	if r.promptError != "" {
		messages = append(messages, fmt.Sprintf("Runtime error: %s", r.promptError))
	}
	return messages
}

func (r *TurnRenderer) SetPromptError(err string) {
	if r != nil {
		r.promptError = strings.TrimSpace(err)
	}
}

func (r *TurnRenderer) RenderActivity(event runtimeactivity.Event, roomID, senderID string) (RenderedActivity, bool) {
	if r == nil {
		return RenderedActivity{}, false
	}
	switch event.Kind {
	case runtimeactivity.EventToolCallStart:
		tool, changed := r.mergeToolSnapshot(event)
		if !changed {
			return RenderedActivity{}, false
		}
		return renderActivityPayload(event, roomID, senderID, toolActivityContent(event, tool))
	case runtimeactivity.EventToolCallUpdate:
		tool, changed := r.mergeToolSnapshot(event)
		if !changed {
			return RenderedActivity{}, false
		}
		return renderActivityPayload(event, roomID, senderID, toolActivityContent(event, tool))
	case runtimeactivity.EventPermissionRequest, runtimeactivity.EventPermissionDecision:
		snapshot, ok := event.Payload.(runtimeactivity.PermissionSnapshot)
		if !ok {
			return RenderedActivity{}, false
		}
		return renderActivityPayload(event, roomID, senderID, permissionActivityContent(event, snapshot))
	default:
		return RenderedActivity{}, false
	}
}

func (r *TurnRenderer) mergeToolSnapshot(event runtimeactivity.Event) (activityTool, bool) {
	toolID := strings.TrimSpace(event.ToolCallID)
	if toolID == "" {
		return activityTool{}, false
	}

	tool := r.toolSnapshots[toolID]
	tool.ID = toolID
	mergeString(&tool.Kind, event.ToolKind)
	mergeString(&tool.Title, event.ToolTitle)
	mergeString(&tool.InputSummary, event.ToolInputSummary)
	mergeString(&tool.OutputSummary, event.ToolOutputSummary)
	if strings.TrimSpace(event.ToolStatus) != "" {
		tool.Status = normalizedToolStatus(event.ToolStatus)
	}
	if tool.Title == "" {
		tool.Title = "Run tool"
	}
	if tool.Status == "" {
		tool.Status = "running"
	}

	signature := toolSignature(tool)
	if r.toolSignatures[toolID] == signature {
		return tool, false
	}
	r.toolSnapshots[toolID] = tool
	r.toolSignatures[toolID] = signature
	return tool, true
}

func displayToolTitle(event runtimeactivity.Event) string {
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

type RenderedActivity struct {
	MessageID string
	Text      string
}

func renderActivityPayload(event runtimeactivity.Event, roomID, senderID string, content any) (RenderedActivity, bool) {
	eventID := activityEventID(event)
	payload := agentActivityPayload{
		Type:           AgentActivityType,
		Version:        AgentActivityVersion,
		EventID:        eventID,
		RoomID:         strings.TrimSpace(roomID),
		Sender:         strings.TrimSpace(senderID),
		OriginServerTS: event.ReceivedAt.UnixMilli(),
		Content:        content,
	}
	if payload.OriginServerTS == 0 {
		payload.OriginServerTS = time.Now().UTC().UnixMilli()
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return RenderedActivity{}, false
	}
	return RenderedActivity{MessageID: eventID, Text: string(data)}, true
}

type agentActivityPayload struct {
	Type           string `json:"type"`
	Version        int    `json:"version"`
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
	ID          string                                      `json:"id"`
	ToolCallID  string                                      `json:"tool_call_id"`
	Title       string                                      `json:"title"`
	Status      string                                      `json:"status"`
	RequestedAt string                                      `json:"requested_at,omitempty"`
	ExpiresAt   string                                      `json:"expires_at,omitempty"`
	Options     []runtimeactivity.PermissionOptionSnapshot  `json:"options,omitempty"`
	Decision    *runtimeactivity.PermissionDecisionSnapshot `json:"decision,omitempty"`
}

func toolActivityContent(event runtimeactivity.Event, tool activityTool) toolActivity {
	if tool.Status == "" {
		tool.Status = "running"
	}
	if tool.Title == "" {
		tool.Title = displayToolTitle(event)
	}
	return toolActivity{
		MsgType: AgentToolMsgType,
		Body:    fmt.Sprintf("Tool %s: %s", tool.Status, tool.Title),
		Runtime: activityRuntime{
			Kind:      displayRuntimeKind(event),
			RuntimeID: strings.TrimSpace(event.RuntimeID),
			SessionID: strings.TrimSpace(event.SessionID),
		},
		Tool: tool,
	}
}

func permissionActivityContent(event runtimeactivity.Event, snapshot runtimeactivity.PermissionSnapshot) permissionActivity {
	status := string(snapshot.Status)
	bodyStatus := "Permission required"
	switch snapshot.Status {
	case runtimeactivity.PermissionStatusAllowed:
		bodyStatus = "Permission allowed"
	case runtimeactivity.PermissionStatusRejected:
		bodyStatus = "Permission rejected"
	case runtimeactivity.PermissionStatusExpired:
		bodyStatus = "Permission expired"
	case runtimeactivity.PermissionStatusCanceled:
		bodyStatus = "Permission canceled"
	}
	return permissionActivity{
		MsgType: AgentPermissionMsgType,
		Body:    fmt.Sprintf("%s: %s", bodyStatus, displayToolTitle(event)),
		Runtime: activityRuntime{
			Kind:      displayRuntimeKind(event),
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

func mergeString(target *string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		*target = value
	}
}

func toolSignature(tool activityTool) string {
	return strings.Join([]string{
		tool.ID,
		tool.Kind,
		tool.Title,
		tool.Status,
		tool.InputSummary,
		tool.OutputSummary,
	}, "\x00")
}

func activityEventID(event runtimeactivity.Event) string {
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

func displayRuntimeKind(event runtimeactivity.Event) string {
	if kind := strings.TrimSpace(event.RuntimeKind); kind != "" {
		return kind
	}
	return "runtime"
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
