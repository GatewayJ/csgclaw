package feishu

import (
	"sync"

	"csgclaw/internal/im"
)

const MessageEventTypeMessageCreated = "message.created"

type MessageEvent struct {
	Type         string      `json:"type"`
	RoomID       string      `json:"room_id,omitempty"`
	MentionBotID string      `json:"mention_bot_id,omitempty"`
	Message      *im.Message `json:"message,omitempty"`
}

type MessageBus struct {
	mu          sync.Mutex
	nextID      int
	subscribers map[int]chan MessageEvent
}

func NewMessageBus() *MessageBus {
	return &MessageBus{
		subscribers: make(map[int]chan MessageEvent),
	}
}

func (b *MessageBus) Subscribe() (<-chan MessageEvent, func()) {
	ch := make(chan MessageEvent, 16)

	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subscribers[id] = ch
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if existing, ok := b.subscribers[id]; ok {
			delete(b.subscribers, id)
			close(existing)
		}
		b.mu.Unlock()
	}

	return ch, cancel
}

func (b *MessageBus) Publish(evt MessageEvent) {
	if b == nil {
		return
	}

	// Keep the lock while performing non-blocking sends so a concurrent cancel
	// cannot close a snapshotted channel between lookup and delivery.
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- evt:
		default:
		}
	}
}
