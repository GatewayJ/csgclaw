package activity

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

type EventKind string

const (
	EventUserMessageDelta EventKind = "user_message_delta"
	EventTextDelta        EventKind = "text_delta"
	EventThoughtDelta     EventKind = "thought_delta"
	EventToolCallStart    EventKind = "tool_call_start"
	EventToolCallUpdate   EventKind = "tool_call_update"
	EventPlanUpdate       EventKind = "plan_update"
	EventActionRequest    EventKind = "action_request"
	EventActionDecision   EventKind = "action_decision"
	EventPromptCompleted  EventKind = "prompt_completed"
	EventPromptFailed     EventKind = "prompt_failed"
)

type Event struct {
	RuntimeKind       string
	RuntimeID         string
	SessionID         string
	Kind              EventKind
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

type Sink interface {
	Publish(Event)
}

type Subscriber interface {
	Subscribe(runtimeID string) (<-chan Event, func())
}

const DefaultEventBuffer = 64

// EventSink fans out normalized runtime activity events to bridge workers.
type EventSink struct {
	mu          sync.Mutex
	nextID      int
	subscribers map[int]subscription
}

type subscription struct {
	runtimeID string
	ch        chan Event
}

func NewEventSink() *EventSink {
	return &EventSink{
		subscribers: make(map[int]subscription),
	}
}

func (s *EventSink) Publish(event Event) {
	if s == nil {
		return
	}

	runtimeID := strings.TrimSpace(event.RuntimeID)

	s.mu.Lock()
	targets := make([]chan Event, 0, len(s.subscribers))
	for _, sub := range s.subscribers {
		if sub.runtimeID != "" && sub.runtimeID != runtimeID {
			continue
		}
		targets = append(targets, sub.ch)
	}
	s.mu.Unlock()

	for _, ch := range targets {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *EventSink) Subscribe(runtimeID string) (<-chan Event, func()) {
	ch := make(chan Event, DefaultEventBuffer)
	if s == nil {
		close(ch)
		return ch, func() {}
	}

	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.subscribers[id] = subscription{
		runtimeID: strings.TrimSpace(runtimeID),
		ch:        ch,
	}
	s.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			if sub, ok := s.subscribers[id]; ok {
				delete(s.subscribers, id)
				close(sub.ch)
			}
			s.mu.Unlock()
		})
	}
	return ch, cancel
}

var (
	ErrActionNotFound       = errors.New("action request not found")
	ErrActionInvalidOption  = errors.New("action option is invalid")
	ErrActionAlreadyDecided = errors.New("action request already decided")
	ErrActionGone           = errors.New("action request is no longer pending")
)

type ActionStatus string

const (
	ActionKindPermission = "permission"

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
}

type ActionDecisionSnapshot struct {
	OptionID  string    `json:"option_id,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	DecidedAt time.Time `json:"decided_at"`
}

type ActionRequestSnapshot struct {
	ID          string                  `json:"id"`
	Kind        string                  `json:"kind"`
	BotID       string                  `json:"bot_id,omitempty"`
	RuntimeID   string                  `json:"runtime_id"`
	SessionID   string                  `json:"session_id"`
	ToolCallID  string                  `json:"tool_call_id"`
	ToolTitle   string                  `json:"title"`
	ToolKind    string                  `json:"tool_kind,omitempty"`
	Status      ActionStatus            `json:"status"`
	RequestedAt time.Time               `json:"requested_at"`
	ExpiresAt   time.Time               `json:"expires_at"`
	Options     []ActionOptionSnapshot  `json:"options,omitempty"`
	Decision    *ActionDecisionSnapshot `json:"decision,omitempty"`
}

type ActionDecider interface {
	Decide(ctx context.Context, botID string, actionID string, optionID string) (ActionRequestSnapshot, error)
}
