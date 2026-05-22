package codex

import (
	"strings"
	"sync"

	"csgclaw/internal/activity"
)

const defaultSessionEventBuffer = 64

// EventSink fans out normalized Codex session events to bridge workers.
type EventSink struct {
	mu          sync.Mutex
	nextID      int
	subscribers map[int]*sessionSubscription
}

type sessionSubscription struct {
	runtimeID string
	ch        chan SessionEvent
	reliable  chan SessionEvent
	done      chan struct{}
	stopped   chan struct{}
}

func NewEventSink() *EventSink {
	return &EventSink{
		subscribers: make(map[int]*sessionSubscription),
	}
}

func (s *EventSink) Publish(event SessionEvent) {
	if s == nil {
		return
	}

	runtimeID := strings.TrimSpace(event.RuntimeID)

	s.mu.Lock()
	targets := make([]*sessionSubscription, 0, len(s.subscribers))
	for _, sub := range s.subscribers {
		if sub.runtimeID != "" && sub.runtimeID != runtimeID {
			continue
		}
		targets = append(targets, sub)
	}
	s.mu.Unlock()

	for _, sub := range targets {
		sub.deliver(event)
	}
}

func (s *EventSink) Subscribe(runtimeID string) (<-chan SessionEvent, func()) {
	ch := make(chan SessionEvent, defaultSessionEventBuffer)
	if s == nil {
		close(ch)
		return ch, func() {}
	}

	s.mu.Lock()
	id := s.nextID
	s.nextID++
	sub := &sessionSubscription{
		runtimeID: strings.TrimSpace(runtimeID),
		ch:        ch,
		reliable:  make(chan SessionEvent, defaultSessionEventBuffer),
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
	}
	s.subscribers[id] = sub
	s.mu.Unlock()
	go sub.pumpReliable()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			if sub, ok := s.subscribers[id]; ok {
				delete(s.subscribers, id)
				close(sub.done)
				<-sub.stopped
				close(sub.ch)
			}
			s.mu.Unlock()
		})
	}
	return ch, cancel
}

func (s *sessionSubscription) deliver(event SessionEvent) {
	if !activity.RuntimeEventRequiresReliableDelivery(event) {
		_ = trySendSessionEvent(s.ch, event)
		return
	}
	s.sendReliable(event)
}

func (s *sessionSubscription) sendReliable(event SessionEvent) {
	select {
	case <-s.done:
	case s.reliable <- event:
	}
}

func (s *sessionSubscription) pumpReliable() {
	defer close(s.stopped)
	for {
		select {
		case <-s.done:
			return
		case event := <-s.reliable:
			if !sendSessionEventUntilDone(s.ch, s.done, event) {
				return
			}
		}
	}
}

func trySendSessionEvent(ch chan SessionEvent, event SessionEvent) (sent bool) {
	defer func() {
		if recover() != nil {
			sent = false
		}
	}()
	select {
	case ch <- event:
		return true
	default:
		return false
	}
}

func sendSessionEventUntilDone(ch chan SessionEvent, done <-chan struct{}, event SessionEvent) (sent bool) {
	defer func() {
		if recover() != nil {
			sent = false
		}
	}()
	select {
	case ch <- event:
		return true
	case <-done:
		return false
	}
}
