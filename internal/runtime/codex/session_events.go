package codex

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentruntime "csgclaw/internal/runtime"

	acp "github.com/coder/acp-go-sdk"
)

func eventFromSessionUpdate(runtimeID string, note acp.SessionNotification) SessionEvent {
	base := SessionEvent{
		RuntimeKind: agentruntime.KindCodex,
		RuntimeID:   strings.TrimSpace(runtimeID),
		SessionID:   strings.TrimSpace(string(note.SessionId)),
		ReceivedAt:  time.Now().UTC(),
		Payload:     note.Update,
	}

	switch update := note.Update; {
	case update.AgentMessageChunk != nil:
		base.Kind = SessionEventTextDelta
		base.MessageID = stringValue(update.AgentMessageChunk.MessageId)
		base.Text = textFromContentBlock(update.AgentMessageChunk.Content)
	case update.AgentThoughtChunk != nil:
		base.Kind = SessionEventThoughtDelta
		base.MessageID = stringValue(update.AgentThoughtChunk.MessageId)
		base.Text = textFromContentBlock(update.AgentThoughtChunk.Content)
	case update.UserMessageChunk != nil:
		base.Kind = SessionEventUserMessageDelta
		base.MessageID = stringValue(update.UserMessageChunk.MessageId)
		base.Text = textFromContentBlock(update.UserMessageChunk.Content)
	case update.ToolCall != nil:
		base.Kind = SessionEventToolCallStart
		base.ToolCallID = strings.TrimSpace(string(update.ToolCall.ToolCallId))
		base.ToolKind = strings.TrimSpace(string(update.ToolCall.Kind))
		base.ToolTitle = strings.TrimSpace(update.ToolCall.Title)
		base.ToolStatus = strings.TrimSpace(string(update.ToolCall.Status))
		base.ToolInputSummary = summarizeToolValue(update.ToolCall.RawInput)
		base.ToolOutputSummary = summarizeToolValue(update.ToolCall.RawOutput)
		base.Payload = update.ToolCall
	case update.ToolCallUpdate != nil:
		base.Kind = SessionEventToolCallUpdate
		base.ToolCallID = strings.TrimSpace(string(update.ToolCallUpdate.ToolCallId))
		base.ToolTitle = stringValue(update.ToolCallUpdate.Title)
		if update.ToolCallUpdate.Kind != nil {
			base.ToolKind = strings.TrimSpace(string(*update.ToolCallUpdate.Kind))
		}
		if update.ToolCallUpdate.Status != nil {
			base.ToolStatus = strings.TrimSpace(string(*update.ToolCallUpdate.Status))
		}
		base.ToolInputSummary = summarizeToolValue(update.ToolCallUpdate.RawInput)
		base.ToolOutputSummary = summarizeToolValue(update.ToolCallUpdate.RawOutput)
		base.Payload = update.ToolCallUpdate
	case update.Plan != nil:
		base.Kind = SessionEventPlanUpdate
		base.Payload = update.Plan
	default:
		base.Kind = SessionEventKind("session_update")
	}

	return base
}

func permissionRequestEvent(state permissionState) SessionEvent {
	snapshot := state.snapshot
	execution := state.execution
	return SessionEvent{
		RuntimeKind:  agentruntime.KindCodex,
		RuntimeID:    strings.TrimSpace(execution.RuntimeID),
		SessionID:    strings.TrimSpace(execution.SessionID),
		Kind:         SessionEventPermissionRequest,
		ReceivedAt:   time.Now().UTC(),
		ToolCallID:   strings.TrimSpace(execution.ToolCallID),
		ToolKind:     strings.TrimSpace(execution.ToolKind),
		ToolTitle:    strings.TrimSpace(snapshot.Title),
		ActionID:     strings.TrimSpace(snapshot.ID),
		ActionStatus: string(snapshot.Status),
		Payload:      snapshot,
	}
}

func permissionDecisionEvent(state permissionState) SessionEvent {
	snapshot := state.snapshot
	execution := state.execution
	event := SessionEvent{
		RuntimeKind:  agentruntime.KindCodex,
		RuntimeID:    strings.TrimSpace(execution.RuntimeID),
		SessionID:    strings.TrimSpace(execution.SessionID),
		Kind:         SessionEventPermissionDecision,
		ReceivedAt:   time.Now().UTC(),
		ToolCallID:   strings.TrimSpace(execution.ToolCallID),
		ToolKind:     strings.TrimSpace(execution.ToolKind),
		ToolTitle:    strings.TrimSpace(snapshot.Title),
		ActionID:     strings.TrimSpace(snapshot.ID),
		ActionStatus: string(snapshot.Status),
		Payload:      snapshot,
	}
	if snapshot.Decision != nil {
		event.ActionOptionID = strings.TrimSpace(snapshot.Decision.OptionID)
		event.ActionOptionKind = strings.TrimSpace(snapshot.Decision.Kind)
	}
	return event
}

func promptCompletedEvent(runtimeID string, sessionID string, resp acp.PromptResponse) SessionEvent {
	return SessionEvent{
		RuntimeKind: agentruntime.KindCodex,
		RuntimeID:   strings.TrimSpace(runtimeID),
		SessionID:   strings.TrimSpace(sessionID),
		Kind:        SessionEventPromptCompleted,
		ReceivedAt:  time.Now().UTC(),
		MessageID:   stringValue(resp.UserMessageId),
		StopReason:  strings.TrimSpace(string(resp.StopReason)),
		Payload:     resp,
	}
}

func promptFailedEvent(runtimeID string, sessionID string, err error) SessionEvent {
	return SessionEvent{
		RuntimeKind: agentruntime.KindCodex,
		RuntimeID:   strings.TrimSpace(runtimeID),
		SessionID:   strings.TrimSpace(sessionID),
		Kind:        SessionEventPromptFailed,
		ReceivedAt:  time.Now().UTC(),
		Error:       errorString(err),
		Payload:     err,
	}
}

func textFromContentBlock(block acp.ContentBlock) string {
	if block.Text != nil {
		return block.Text.Text
	}
	if block.ResourceLink != nil {
		return strings.TrimSpace(block.ResourceLink.Name) + " " + strings.TrimSpace(block.ResourceLink.Uri)
	}
	if block.Resource != nil {
		if block.Resource.Resource.TextResourceContents != nil {
			return block.Resource.Resource.TextResourceContents.Text
		}
	}
	return ""
}

func permissionToolKind(tool acp.ToolCallUpdate) string {
	if tool.Kind == nil {
		return ""
	}
	return strings.TrimSpace(string(*tool.Kind))
}

func summarizeToolValue(value any) string {
	if value == nil {
		return ""
	}
	sanitized := redactToolValue(value)
	data, err := json.Marshal(sanitized)
	if err != nil {
		return ""
	}
	return truncateSummary(string(data), 240)
}

func redactToolValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSecretishKey(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = redactToolValue(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, redactToolValue(item))
		}
		return out
	default:
		return value
	}
}

func isSecretishKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(key, "token") ||
		strings.Contains(key, "secret") ||
		strings.Contains(key, "password") ||
		strings.Contains(key, "api_key") ||
		strings.Contains(key, "apikey")
}

func truncateSummary(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit]) + "..."
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", err))
}
