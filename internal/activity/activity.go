package activity

import (
	"context"
	"errors"
	"time"
)

var (
	ErrActionNotFound       = errors.New("action request not found")
	ErrActionInvalidOption  = errors.New("action option is invalid")
	ErrActionAlreadyDecided = errors.New("action request already decided")
	ErrActionGone           = errors.New("action request is no longer pending")
)

type ActionStatus string

const (
	ActionKindPermission = "permission"

	ActionOptionScopeAgent = "agent"

	ActionStatusPending  ActionStatus = "pending"
	ActionStatusAllowed  ActionStatus = "allowed"
	ActionStatusRejected ActionStatus = "rejected"
	ActionStatusExpired  ActionStatus = "expired"
	ActionStatusCanceled ActionStatus = "canceled"
)

type ActionOptionSnapshot struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Scope string `json:"scope,omitempty"`
}

type ActionDecisionSnapshot struct {
	OptionID  string    `json:"option_id,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	DecidedAt time.Time `json:"decided_at"`
}

type ActivitySnapshot struct {
	ID          string                  `json:"id"`
	Kind        string                  `json:"kind"`
	Title       string                  `json:"title"`
	Status      ActionStatus            `json:"status"`
	RequestedAt time.Time               `json:"requested_at"`
	ExpiresAt   time.Time               `json:"expires_at"`
	Options     []ActionOptionSnapshot  `json:"options,omitempty"`
	Decision    *ActionDecisionSnapshot `json:"decision,omitempty"`
}

type ExecutionRef struct {
	RuntimeKind string
	RuntimeID   string
	SessionID   string
	ToolCallID  string
	ToolKind    string
}

type ActivityDecisionRequest struct {
	Channel    string
	ActivityID string
	OptionID   string
}

type ActivityDecider interface {
	Decide(ctx context.Context, req ActivityDecisionRequest) (ActivitySnapshot, error)
}

type RuntimeEventKind string

const (
	RuntimeEventUserMessageDelta RuntimeEventKind = "user_message_delta"
	RuntimeEventTextDelta        RuntimeEventKind = "text_delta"
	RuntimeEventThoughtDelta     RuntimeEventKind = "thought_delta"
	RuntimeEventToolCallStart    RuntimeEventKind = "tool_call_start"
	RuntimeEventToolCallUpdate   RuntimeEventKind = "tool_call_update"
	RuntimeEventPlanUpdate       RuntimeEventKind = "plan_update"
	RuntimeEventActionRequest    RuntimeEventKind = "action_request"
	RuntimeEventActionDecision   RuntimeEventKind = "action_decision"
	RuntimeEventPromptCompleted  RuntimeEventKind = "prompt_completed"
	RuntimeEventPromptFailed     RuntimeEventKind = "prompt_failed"
)

type RuntimeEvent struct {
	RuntimeKind       string
	RuntimeID         string
	SessionID         string
	Kind              RuntimeEventKind
	ReceivedAt        time.Time
	MessageID         string
	Text              string
	ToolCallID        string
	ToolKind          string
	ToolTitle         string
	ToolStatus        string
	ToolInputSummary  string
	ToolOutputSummary string
	ActionID          string
	ActionStatus      string
	ActionOptionID    string
	ActionOptionKind  string
	StopReason        string
	Error             string
	Payload           any
}

type RuntimeEventSink interface {
	Publish(RuntimeEvent)
}

type RuntimeEventSubscriber interface {
	Subscribe(runtimeID string) (<-chan RuntimeEvent, func())
}

func RuntimeEventRequiresReliableDelivery(event RuntimeEvent) bool {
	switch event.Kind {
	case RuntimeEventActionRequest, RuntimeEventActionDecision:
		return true
	default:
		return false
	}
}
