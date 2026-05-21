package codexbridge

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	runtimecodex "csgclaw/internal/runtime/codex"

	acp "github.com/coder/acp-go-sdk"
)

const (
	defaultQueueSize    = 32
	defaultSeenWindow   = 256
	defaultPromptSettle = 150 * time.Millisecond
)

type Binding struct {
	BotID      string
	RuntimeID  string
	SessionID  string
	PromptMeta map[string]any
}

type SessionPrompter interface {
	Prompt(ctx context.Context, handle runtimecodex.SessionHandle, req acp.PromptRequest) (acp.PromptResponse, error)
}

type Service struct {
	client         BotClient
	prompter       SessionPrompter
	events         *EventSink
	reconnectDelay time.Duration
	queueSize      int
	seenWindow     int
	promptSettle   time.Duration

	mu      sync.Mutex
	workers map[string]*worker
}

func NewService(client BotClient, prompter SessionPrompter, events *EventSink) *Service {
	return &Service{
		client:         client,
		prompter:       prompter,
		events:         events,
		reconnectDelay: defaultReconnectDelay,
		queueSize:      defaultQueueSize,
		seenWindow:     defaultSeenWindow,
		promptSettle:   defaultPromptSettle,
		workers:        make(map[string]*worker),
	}
}

func (s *Service) StartBot(ctx context.Context, binding Binding) error {
	if s == nil {
		return fmt.Errorf("codex bridge service is required")
	}
	if s.client == nil {
		return fmt.Errorf("bot client is required")
	}
	if s.prompter == nil {
		return fmt.Errorf("session prompter is required")
	}
	if s.events == nil {
		return fmt.Errorf("event sink is required")
	}

	binding.BotID = strings.TrimSpace(binding.BotID)
	binding.RuntimeID = strings.TrimSpace(binding.RuntimeID)
	binding.SessionID = strings.TrimSpace(binding.SessionID)
	if binding.BotID == "" || binding.RuntimeID == "" {
		return fmt.Errorf("bot id and runtime id are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing := s.workers[binding.BotID]; existing != nil {
		if sameBinding(existing.binding, binding) {
			return nil
		}
		existing.cancel()
	}

	if ctx == nil {
		ctx = context.Background()
	}
	workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	w := &worker{
		service: s,
		binding: binding,
		queue:   make(chan BotEvent, s.queueSize),
		queued:  make(map[string]struct{}),
		seen:    newRecentSet(s.seenWindow),
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	s.workers[binding.BotID] = w
	go w.run(workerCtx)
	return nil
}

func (s *Service) StopBot(botID string) {
	if s == nil {
		return
	}
	botID = strings.TrimSpace(botID)
	if botID == "" {
		return
	}

	s.mu.Lock()
	w := s.workers[botID]
	delete(s.workers, botID)
	s.mu.Unlock()

	if w != nil {
		w.cancel()
		<-w.done
	}
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	workers := make([]*worker, 0, len(s.workers))
	for _, w := range s.workers {
		workers = append(workers, w)
	}
	s.workers = make(map[string]*worker)
	s.mu.Unlock()

	for _, w := range workers {
		w.cancel()
	}
	for _, w := range workers {
		<-w.done
	}
}

type worker struct {
	service *Service
	binding Binding
	queue   chan BotEvent
	queued  map[string]struct{}
	seen    *recentSet
	cancel  context.CancelFunc
	done    chan struct{}

	mu         sync.Mutex
	processing string
	lastEvent  string
}

func (w *worker) run(ctx context.Context) {
	defer close(w.done)

	eventCh, cancelEvents := w.service.events.Subscribe(w.binding.RuntimeID)
	defer cancelEvents()

	go w.pumpEvents(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-w.queue:
			w.beginProcessing(evt.MessageID)
			_ = w.handleEvent(ctx, evt, eventCh)
			w.finishProcessing(evt.MessageID)
		}
	}
}

func (w *worker) pumpEvents(ctx context.Context) {
	for {
		events, errs := w.service.client.StreamEvents(ctx, w.binding.BotID, w.lastEventID())
		closed := false
		for !closed {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-events:
				if !ok {
					closed = true
					continue
				}
				if strings.TrimSpace(evt.MessageID) != "" {
					w.setLastEventID(evt.MessageID)
				}
				w.enqueue(ctx, evt)
			case err, ok := <-errs:
				if ok && err != nil {
					closed = true
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.service.reconnectDelay):
		}
	}
}

func (w *worker) enqueue(ctx context.Context, evt BotEvent) {
	if !w.accept(evt.MessageID) {
		return
	}
	select {
	case <-ctx.Done():
	case w.queue <- evt:
	}
}

func (w *worker) handleEvent(ctx context.Context, evt BotEvent, runtimeEvents <-chan runtimecodex.SessionEvent) error {
	req := acp.PromptRequest{
		SessionId: acp.SessionId(w.binding.SessionID),
		Prompt:    []acp.ContentBlock{acp.TextBlock(evt.Text)},
		Meta:      cloneMeta(w.binding.PromptMeta),
	}
	renderer := newTurnRenderer()

	type promptResult struct {
		err error
	}
	promptDone := make(chan promptResult, 1)
	go func() {
		_, err := w.service.prompter.Prompt(ctx, runtimecodex.SessionHandle{RuntimeID: w.binding.RuntimeID}, req)
		promptDone <- promptResult{err: err}
	}()

	promptReturned := false
	settleTimer := time.NewTimer(time.Hour)
	defer settleTimer.Stop()
	if !settleTimer.Stop() {
		select {
		case <-settleTimer.C:
		default:
		}
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-runtimeEvents:
			if !ok {
				return fmt.Errorf("codex event sink closed")
			}
			if !matchesBinding(event, w.binding) {
				continue
			}
			if renderer.ShouldRenderActivity(event) {
				if activity, ok := renderActivity(event, w.binding, evt.RoomID, w.binding.BotID); ok {
					if err := w.sendActivity(ctx, evt.RoomID, activity); err != nil {
						return err
					}
				}
			}
			for _, text := range renderer.Apply(event) {
				if err := w.sendMessage(ctx, evt.RoomID, text); err != nil {
					return err
				}
			}
			if isTerminalEvent(event.Kind) && promptReturned {
				return w.flushTurn(ctx, evt.RoomID, renderer)
			}
		case result := <-promptDone:
			promptReturned = true
			if result.err != nil {
				renderer.promptError = strings.TrimSpace(result.err.Error())
				return w.flushTurn(ctx, evt.RoomID, renderer)
			}
			settleTimer.Reset(w.service.promptSettle)
		case <-settleTimer.C:
			if promptReturned {
				return w.flushTurn(ctx, evt.RoomID, renderer)
			}
		}
	}
}

func (w *worker) flushTurn(ctx context.Context, roomID string, renderer *turnRenderer) error {
	for _, text := range renderer.FinalMessages() {
		if err := w.sendMessage(ctx, roomID, text); err != nil {
			return err
		}
	}
	return nil
}

func (w *worker) sendMessage(ctx context.Context, roomID, text string) error {
	return w.sendMessageRequest(ctx, SendMessageRequest{
		RoomID: roomID,
		Text:   text,
	})
}

func (w *worker) sendActivity(ctx context.Context, roomID string, activity renderedActivity) error {
	return w.sendMessageRequest(ctx, SendMessageRequest{
		RoomID:    roomID,
		Text:      activity.Text,
		MessageID: activity.MessageID,
	})
}

func (w *worker) sendMessageRequest(ctx context.Context, req SendMessageRequest) error {
	req.Text = strings.TrimSpace(req.Text)
	req.MessageID = strings.TrimSpace(req.MessageID)
	if req.Text == "" {
		return nil
	}
	return w.service.client.SendMessage(ctx, w.binding.BotID, req)
}

func (w *worker) accept(messageID string) bool {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return true
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen.Has(messageID) {
		return false
	}
	if _, ok := w.queued[messageID]; ok {
		return false
	}
	if w.processing == messageID {
		return false
	}
	w.queued[messageID] = struct{}{}
	return true
}

func (w *worker) beginProcessing(messageID string) {
	messageID = strings.TrimSpace(messageID)
	w.mu.Lock()
	delete(w.queued, messageID)
	w.processing = messageID
	w.mu.Unlock()
}

func (w *worker) finishProcessing(messageID string) {
	messageID = strings.TrimSpace(messageID)
	w.mu.Lock()
	if messageID != "" {
		w.seen.Add(messageID)
	}
	if w.processing == messageID {
		w.processing = ""
	}
	w.mu.Unlock()
}

func (w *worker) lastEventID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastEvent
}

func (w *worker) setLastEventID(messageID string) {
	w.mu.Lock()
	w.lastEvent = strings.TrimSpace(messageID)
	w.mu.Unlock()
}

func matchesBinding(event runtimecodex.SessionEvent, binding Binding) bool {
	if strings.TrimSpace(event.RuntimeID) != strings.TrimSpace(binding.RuntimeID) {
		return false
	}
	if strings.TrimSpace(binding.SessionID) == "" {
		return true
	}
	return strings.TrimSpace(event.SessionID) == strings.TrimSpace(binding.SessionID)
}

func isTerminalEvent(kind runtimecodex.SessionEventKind) bool {
	return kind == runtimecodex.SessionEventPromptCompleted || kind == runtimecodex.SessionEventPromptFailed
}

func cloneMeta(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func sameBinding(left, right Binding) bool {
	return strings.TrimSpace(left.BotID) == strings.TrimSpace(right.BotID) &&
		strings.TrimSpace(left.RuntimeID) == strings.TrimSpace(right.RuntimeID) &&
		strings.TrimSpace(left.SessionID) == strings.TrimSpace(right.SessionID)
}

type recentSet struct {
	limit int
	order []string
	items map[string]struct{}
}

func newRecentSet(limit int) *recentSet {
	if limit <= 0 {
		limit = defaultSeenWindow
	}
	return &recentSet{
		limit: limit,
		items: make(map[string]struct{}),
	}
}

func (s *recentSet) Has(key string) bool {
	if s == nil || key == "" {
		return false
	}
	_, ok := s.items[key]
	return ok
}

func (s *recentSet) Add(key string) {
	if s == nil || key == "" {
		return
	}
	if _, ok := s.items[key]; ok {
		return
	}
	s.items[key] = struct{}{}
	s.order = append(s.order, key)
	if len(s.order) <= s.limit {
		return
	}
	oldest := s.order[0]
	s.order = s.order[1:]
	delete(s.items, oldest)
}
