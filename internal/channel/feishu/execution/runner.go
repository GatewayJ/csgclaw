package execution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"

	"csgclaw/internal/agentengine"
	channeltypes "csgclaw/internal/channel"
	feishuctx "csgclaw/internal/channel/feishu/context"
	"csgclaw/internal/channel/feishu/presentation"
	feishustate "csgclaw/internal/channel/feishu/state"
	"csgclaw/internal/slashcommand"
)

const processingPinEmoji = "Pin"

type FilePreparer interface {
	Prepare(context.Context, channeltypes.InboundMessage) ([]agentengine.InputPart, func(), error)
}

type Notifier interface {
	Notify()
}

type runnerState interface {
	Put(channeltypes.TurnRecord) error
	Get(string) (channeltypes.TurnRecord, bool)
	Enqueue(channeltypes.DeliveryIntent) error
	BeginTurn(channeltypes.TurnRecord) error
	AppendTurnDeliveries(string, uint64, ...channeltypes.DeliveryIntent) error
	FinishTurn(string, channeltypes.TurnStatus, ...channeltypes.DeliveryIntent) error
}

type RunnerOptions struct {
	Engine       agentengine.Interface
	State        *feishustate.Store
	Files        FilePreparer
	Notifier     Notifier
	Presentation presentation.Mode
}

// Runner is the only Feishu component allowed to invoke Agent Engine Run. It
// records only process-local presentation dependencies and never performs
// Feishu network calls.
type Runner struct {
	engine   agentengine.Interface
	state    runnerState
	files    FilePreparer
	notifier Notifier
	mode     presentation.Mode

	mu     sync.Mutex
	active map[string]*activeRun
	runs   map[*activeRun]struct{}
}

type activeRun struct {
	agentID       string
	key           string
	turnID        string
	mode          presentation.Mode
	engineEntered bool // guarded by Runner.mu
	cancel        context.CancelFunc
	done          chan struct{}
}

func NewRunner(options RunnerOptions) (*Runner, error) {
	if options.Engine == nil {
		return nil, fmt.Errorf("feishu runner: agent engine is required")
	}
	if options.State == nil {
		return nil, fmt.Errorf("feishu runner: memory state is required")
	}
	return &Runner{
		engine:   options.Engine,
		state:    options.State,
		files:    options.Files,
		notifier: options.Notifier,
		mode:     presentation.NormalizeMode(string(options.Presentation)),
		active:   make(map[string]*activeRun),
		runs:     make(map[*activeRun]struct{}),
	}, nil
}

// Submit starts an Engine Run from the binding worker context. It does not
// queue conversations; Agent Engine AdmissionSupersede owns replacement.
func (r *Runner) Submit(ctx context.Context, message channeltypes.InboundMessage) error {
	if err := validateMessage(message); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	record := turnRecord(message, channeltypes.TurnAccepted)
	if err := r.state.Put(record); err != nil {
		return fmt.Errorf("record accepted Feishu turn: %w", err)
	}
	mode := r.presentationModeForTurn(message.TurnID)
	if isChatReply(message) {
		if err := r.enqueueInitialPresentation(message, mode); err != nil {
			return err
		}
		if err := r.enqueueProcessingReaction(message); err != nil {
			return err
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	active := &activeRun{
		agentID: message.AgentID,
		key:     message.ConversationKey,
		turnID:  message.TurnID,
		mode:    mode,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	r.mu.Lock()
	previous := r.active[message.ConversationKey]
	r.active[message.ConversationKey] = active
	r.runs[active] = struct{}{}
	// The channel owns only work that has not crossed into Agent Engine. The
	// transition to engineEntered uses this same lock, so a replacement either
	// cancels preflight or leaves an Engine-visible Turn to AdmissionSupersede.
	canceledPreviousPreflight := previous != nil && !previous.engineEntered
	if canceledPreviousPreflight {
		previous.cancel()
	}
	r.mu.Unlock()
	slog.Debug("accepted Feishu turn",
		messageLogAttrs(message,
			"presentation_mode", mode,
			"canceled_previous_preflight", canceledPreviousPreflight,
		)...)
	go r.run(runCtx, active, message)
	return nil
}

func (r *Runner) run(ctx context.Context, active *activeRun, message channeltypes.InboundMessage) {
	defer func() {
		active.cancel()
		close(active.done)
		r.mu.Lock()
		if r.active[active.key] == active {
			delete(r.active, active.key)
		}
		delete(r.runs, active)
		r.mu.Unlock()
	}()
	input := make([]agentengine.InputPart, 0, len(message.Files)+1)
	if prompt := feishuctx.MessagePrompt(message); prompt != "" {
		input = append(input, agentengine.InputPart{Kind: agentengine.InputPartText, Text: prompt})
	}
	cleanupFiles := func() {}
	if len(message.Files) > 0 {
		if r.files == nil {
			slog.Warn("prepare Feishu turn files failed",
				messageLogAttrs(message,
					"error_code", agentengine.ErrorFileUnavailable,
					"error", "Feishu attachment handling is unavailable",
				)...)
			if err := r.finalize(message, agentengine.TurnResult{
				Status: agentengine.TurnFailed,
				Error:  &agentengine.TurnError{Code: agentengine.ErrorFileUnavailable, Message: "Feishu attachment handling is unavailable"},
			}, true, presentation.Rendered{}); err != nil {
				r.logFinalizeError(message, err)
			}
			return
		}
		files, cleanup, err := r.files.Prepare(ctx, message)
		if cleanup != nil {
			cleanupFiles = cleanup
		}
		if err != nil {
			cleanupFiles()
			slog.Warn("prepare Feishu turn files failed",
				messageLogAttrs(message,
					"error_code", agentengine.ErrorCodeOf(err),
					"error", err,
				)...)
			if finalizeErr := r.finalize(message, resultFromError(err), true, presentation.Rendered{}); finalizeErr != nil {
				r.logFinalizeError(message, finalizeErr)
			}
			return
		}
		input = append(input, files...)
	}
	defer cleanupFiles()
	if len(input) == 0 {
		slog.Warn("reject Feishu turn without supported input",
			messageLogAttrs(message,
				"error_code", agentengine.ErrorInvalidRequest,
			)...)
		if err := r.finalize(message, agentengine.TurnResult{
			Status: agentengine.TurnFailed,
			Error:  &agentengine.TurnError{Code: agentengine.ErrorInvalidRequest, Message: "Feishu message contains no supported text or attachment"},
		}, true, presentation.Rendered{}); err != nil {
			r.logFinalizeError(message, err)
		}
		return
	}
	if err := r.state.BeginTurn(turnRecord(message, channeltypes.TurnRunning)); err != nil {
		slog.Error("record running Feishu turn failed", messageLogAttrs(message, "error", err)...)
		return
	}
	if !r.markEngineEntered(ctx, active) {
		slog.Debug("cancel Feishu turn before Agent Engine run", messageLogAttrs(message)...)
		if err := r.finalize(message, resultFromError(context.Canceled), true, presentation.Rendered{}); err != nil {
			r.logFinalizeError(message, err)
		}
		return
	}

	slog.Debug("start Feishu Agent Engine run",
		messageLogAttrs(message,
			"input_part_count", len(input),
			"presentation_mode", active.mode,
		)...)
	progress := presentation.NewProgress(active.mode, message.TurnID, message.ConversationKey,
		strings.TrimSpace(message.Source.ThreadID))
	result := r.engine.Conversations(message.AgentID).Run(ctx, agentengine.TurnRequest{
		ID:              agentengine.TurnID(message.TurnID),
		ConversationKey: agentengine.ConversationKey(message.ConversationKey),
		Input:           input,
		Admission:       agentengine.AdmissionSupersede,
		Continuation:    agentengine.ContinuationCreateOrResume,
		Interaction:     agentengine.InteractionSkipUserInput,
	}, agentengine.EventSinkFunc(func(_ context.Context, event agentengine.TurnEvent) error {
		if !isChatReply(message) {
			return nil
		}
		rendered, flush := progress.Observe(event)
		if !flush {
			return nil
		}
		intents := r.presentationUpdateIntents(message, event.Sequence, false, rendered)
		if err := r.state.AppendTurnDeliveries(message.TurnID, event.Sequence, intents...); err != nil {
			return err
		}
		r.notify()
		return nil
	}))
	result = normalizeTerminalResult(result)
	r.logTerminalResult(message, result)
	if err := r.finalize(message, result, true, progress.Finalize(presentationResult(result))); err != nil {
		r.logFinalizeError(message, err)
	}
}

// Reset handles /new through Engine's atomic active-Turn Reset operation.
func (r *Runner) Reset(ctx context.Context, message channeltypes.InboundMessage) error {
	if err := validateMessage(message); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.state.Put(turnRecord(message, channeltypes.TurnAccepted)); err != nil {
		return fmt.Errorf("record accepted Feishu reset: %w", err)
	}
	slog.Debug("accepted Feishu reset", messageLogAttrs(message)...)
	if active := r.current(message.ConversationKey); active != nil {
		// Engine cannot see attachment preparation, so cancel it before crossing
		// the atomic Reset boundary. A late Run observes its canceled context.
		slog.Debug("cancel active Feishu turn before reset",
			messageLogAttrs(message,
				"active_turn_id", active.turnID,
			)...)
		active.cancel()
	}
	if err := r.state.BeginTurn(turnRecord(message, channeltypes.TurnRunning)); err != nil {
		return fmt.Errorf("record running Feishu reset: %w", err)
	}
	err := r.engine.Conversations(message.AgentID).Reset(ctx, agentengine.ConversationKey(message.ConversationKey))
	if err != nil {
		result := resultFromError(err)
		slog.Warn("Feishu Agent Engine reset failed",
			messageLogAttrs(message, resultLogAttrs(result)...,
			)...)
		return r.finalize(message, result, false, presentation.Rendered{})
	}
	intent := r.textIntent(message, message.TurnID+":reset", 1, "Cleared my internal history for this conversation. The IM room messages were not cleared.")
	if err := r.state.FinishTurn(message.TurnID, channeltypes.TurnSucceeded, intent); err != nil {
		return fmt.Errorf("record terminal Feishu reset: %w", err)
	}
	slog.Debug("Feishu Agent Engine reset completed", messageLogAttrs(message, "status", channeltypes.TurnSucceeded)...)
	r.notify()
	return nil
}

// Cancel stops channel-side preparation for the exact active Turn and then
// delegates cancellation to Agent Engine. The local cancellation matters in
// the short interval before Engine.Run is entered (for example while an
// attachment is downloading); it never substitutes a Runtime call.
func (r *Runner) Cancel(ctx context.Context, agentID, conversationKey, turnID string) error {
	agentID = strings.TrimSpace(agentID)
	conversationKey = strings.TrimSpace(conversationKey)
	turnID = strings.TrimSpace(turnID)
	if agentID == "" || conversationKey == "" || turnID == "" {
		return &agentengine.TurnError{Code: agentengine.ErrorInvalidRequest, Message: "agent, conversation, and turn IDs are required"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	slog.Debug("cancel Feishu Agent Engine turn requested",
		"agent_id", agentID,
		"conversation_key", conversationKey,
		"turn_id", turnID,
	)
	active := r.current(conversationKey)
	if active != nil && active.agentID == agentID && active.turnID == turnID {
		active.cancel()
	}
	if err := r.engine.Conversations(agentID).Cancel(ctx,
		agentengine.ConversationKey(conversationKey), agentengine.TurnID(turnID)); err != nil {
		return err
	}
	if active != nil && active.agentID == agentID && active.turnID == turnID {
		select {
		case <-active.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (r *Runner) finalize(message channeltypes.InboundMessage, result agentengine.TurnResult, includePresentation bool, terminal presentation.Rendered) error {
	result = normalizeTerminalResult(result)
	mode := r.presentationModeForTurn(message.TurnID)
	if terminal.Mode != "" {
		mode = presentation.NormalizeMode(string(terminal.Mode))
	}
	status := channeltypes.TurnFailed
	switch result.Status {
	case agentengine.TurnSucceeded:
		status = channeltypes.TurnSucceeded
	case agentengine.TurnCanceled:
		status = channeltypes.TurnCanceled
	case agentengine.TurnFailed:
		status = channeltypes.TurnFailed
	}
	if result.Error != nil {
		if status == channeltypes.TurnSucceeded {
			status = channeltypes.TurnFailed
		}
	}
	finalSequence := uint64(1)
	if record, ok := r.state.Get(message.TurnID); ok {
		finalSequence = record.LastSequence + 1
	}

	intents := make([]channeltypes.DeliveryIntent, 0, 3)
	// Chat replies finish by updating the one presentation message created for
	// the Turn. Emitting result.Output separately here would duplicate the same
	// answer already rendered in the terminal Markdown/Card snapshot. Document
	// comments have no presentation message, so they still need one reply.
	if status != channeltypes.TurnCanceled && isCommentReply(message) {
		text := strings.TrimSpace(result.Output)
		if status == channeltypes.TurnFailed {
			text = userFacingError(result.Error)
		} else if text == "" {
			text = "Done."
		}
		intents = append(intents, r.commentIntent(message, message.TurnID+":final", finalSequence, text))
	}
	if includePresentation && isChatReply(message) {
		if terminal.Mode == "" {
			terminal = presentation.Terminal(mode, presentationResult(result))
		}
		intents = append(intents, r.presentationUpdateIntents(message, finalSequence, true, terminal)...)
		if cleanup, ok := r.reactionCleanupIntent(message); ok {
			intents = append(intents, cleanup)
		}
	}
	// Agent Engine currently returns terminal state only as TurnResult, not a
	// terminal TurnEvent. Record the result and terminal deliveries together in
	// process memory without inventing an Engine event.
	if err := r.state.FinishTurn(message.TurnID, status, intents...); err != nil {
		return fmt.Errorf("record terminal Feishu turn: %w", err)
	}
	r.notify()
	return nil
}

// presentationModeForTurn preserves the mode recorded by the in-memory create
// intent while the binding is running.
func (r *Runner) presentationModeForTurn(turnID string) presentation.Mode {
	lookup, ok := r.state.(interface {
		Delivery(string) (channeltypes.DeliveryIntent, bool)
	})
	if !ok {
		return r.mode
	}
	if intent, found := lookup.Delivery(cardCreateID(turnID)); found && intent.Kind == channeltypes.DeliveryCard {
		return presentation.ModeCard
	}
	if intent, found := lookup.Delivery(presentationCreateID(presentation.ModeMarkdown, turnID)); found && intent.Kind == channeltypes.DeliveryMarkdown {
		return presentation.ModeMarkdown
	}
	return r.mode
}

func normalizeTerminalResult(result agentengine.TurnResult) agentengine.TurnResult {
	switch result.Status {
	case agentengine.TurnSucceeded:
		if result.Error != nil {
			result.Status = agentengine.TurnFailed
		}
	case agentengine.TurnFailed, agentengine.TurnCanceled:
	default:
		result.Status = agentengine.TurnFailed
		result.Error = &agentengine.TurnError{
			Code:    agentengine.ErrorRuntimeFailed,
			Message: "Agent Engine returned no terminal status",
		}
	}
	return result
}

func presentationResult(result agentengine.TurnResult) agentengine.TurnResult {
	result = normalizeTerminalResult(result)
	if result.Status == agentengine.TurnFailed {
		result.Error = &agentengine.TurnError{
			Code:    agentengine.ErrorCodeOf(result.Error),
			Message: userFacingError(result.Error),
		}
	}
	return result
}

func (r *Runner) commentIntent(message channeltypes.InboundMessage, id string, sequence uint64, text string) channeltypes.DeliveryIntent {
	intent := baseIntent(message, id, sequence)
	intent.Kind = channeltypes.DeliveryCommentReply
	intent.Text = truncateRunes(strings.TrimSpace(text), 2000)
	return intent
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if limit <= 0 {
		return ""
	}
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

func (r *Runner) textIntent(message channeltypes.InboundMessage, id string, sequence uint64, text string) channeltypes.DeliveryIntent {
	text = strings.TrimSpace(text)
	intent := baseIntent(message, id, sequence)
	intent.Kind = channeltypes.DeliveryText
	intent.Text = text
	return intent
}

func (r *Runner) enqueueInitialPresentation(message channeltypes.InboundMessage, mode presentation.Mode) error {
	rendered := presentation.Initial(mode, message.TurnID, message.ConversationKey,
		strings.TrimSpace(message.Source.ThreadID))
	intent := baseIntent(message, presentationCreateID(rendered.Mode, message.TurnID), 0)
	switch rendered.Mode {
	case presentation.ModeMarkdown:
		intent.Kind = channeltypes.DeliveryMarkdown
		intent.Text = rendered.Markdown
	case presentation.ModeCard:
		intent.Kind = channeltypes.DeliveryCard
		intent.Card = rendered.Card
	default:
		return fmt.Errorf("unsupported Feishu presentation mode %q", rendered.Mode)
	}
	if err := r.state.Enqueue(intent); err != nil {
		return fmt.Errorf("queue initial Feishu presentation: %w", err)
	}
	r.notify()
	return nil
}

func (r *Runner) presentationUpdateIntent(message channeltypes.InboundMessage, sequence uint64, final bool, rendered presentation.Rendered) channeltypes.DeliveryIntent {
	id := presentationUpdateID(rendered.Mode, message.TurnID, sequence, final)
	intent := baseIntent(message, id, sequence)
	intent.RelatedID = presentationCreateID(rendered.Mode, message.TurnID)
	switch rendered.Mode {
	case presentation.ModeMarkdown:
		intent.Kind = channeltypes.DeliveryMarkdownUpdate
		intent.Text = rendered.Markdown
	case presentation.ModeCard:
		intent.Kind = channeltypes.DeliveryCardUpdate
		intent.Card = rendered.Card
	}
	return intent
}

// presentationUpdateIntents turns one rendered Markdown snapshot into ordered
// message parts when it exceeds Feishu's rich-message limit. The first part
// continues to update the original presentation message; later parts get their
// own reply messages and are updated independently on later snapshots.
func (r *Runner) presentationUpdateIntents(message channeltypes.InboundMessage, sequence uint64, final bool, rendered presentation.Rendered) []channeltypes.DeliveryIntent {
	if rendered.Mode != presentation.ModeMarkdown || len(rendered.MarkdownParts) <= 1 {
		return []channeltypes.DeliveryIntent{r.presentationUpdateIntent(message, sequence, final, rendered)}
	}
	intents := make([]channeltypes.DeliveryIntent, 0, len(rendered.MarkdownParts))
	for index, text := range rendered.MarkdownParts {
		part := index + 1
		if part == 1 {
			first := rendered
			first.Markdown = text
			first.MarkdownParts = nil
			intents = append(intents, r.presentationUpdateIntent(message, sequence, final, first))
			continue
		}
		createID := markdownContinuationCreateID(message.TurnID, part)
		if r.deliveryExists(createID) {
			intent := baseIntent(message, markdownContinuationUpdateID(message.TurnID, part, sequence, final), sequence)
			intent.Kind = channeltypes.DeliveryMarkdownUpdate
			intent.RelatedID = createID
			intent.Text = text
			intents = append(intents, intent)
			continue
		}
		previousCreateID := presentationCreateID(presentation.ModeMarkdown, message.TurnID)
		if part > 2 {
			previousCreateID = markdownContinuationCreateID(message.TurnID, part-1)
		}
		intent := baseIntent(message, createID, sequence)
		intent.Kind = channeltypes.DeliveryMarkdown
		intent.RelatedID = previousCreateID
		intent.Text = text
		intents = append(intents, intent)
	}
	return intents
}

func (r *Runner) deliveryExists(id string) bool {
	lookup, ok := r.state.(interface {
		Delivery(string) (channeltypes.DeliveryIntent, bool)
	})
	if !ok {
		return false
	}
	_, found := lookup.Delivery(id)
	return found
}

func (r *Runner) enqueueProcessingReaction(message channeltypes.InboundMessage) error {
	if strings.TrimSpace(message.Source.MessageID) == "" {
		return nil
	}
	intent := baseIntent(message, processingReactionID(message.TurnID), 0)
	intent.Kind = channeltypes.DeliveryReactionAdd
	intent.MessageID = message.Source.MessageID
	intent.EmojiType = processingPinEmoji
	if err := r.state.Enqueue(intent); err != nil {
		return fmt.Errorf("queue Feishu processing reaction: %w", err)
	}
	r.notify()
	return nil
}

func (r *Runner) reactionCleanupIntent(message channeltypes.InboundMessage) (channeltypes.DeliveryIntent, bool) {
	if strings.TrimSpace(message.Source.MessageID) == "" {
		return channeltypes.DeliveryIntent{}, false
	}
	intent := baseIntent(message, message.TurnID+":reaction:delete", math.MaxUint64)
	intent.Kind = channeltypes.DeliveryReactionDelete
	intent.MessageID = message.Source.MessageID
	intent.RelatedID = processingReactionID(message.TurnID)
	return intent, true
}

func baseIntent(message channeltypes.InboundMessage, id string, sequence uint64) channeltypes.DeliveryIntent {
	threadID := strings.TrimSpace(message.Source.ThreadID)
	replyTo := ""
	if threadID != "" {
		replyTo = firstNonEmpty(message.Source.RootID, message.Source.ParentID, message.Source.MessageID)
	} else if strings.TrimSpace(message.Source.RootID) != "" || strings.TrimSpace(message.Source.ParentID) != "" {
		// Reply to an ordinary quoted message without asking Feishu to create a
		// topic. ReplyInThread is derived from the real ThreadID downstream.
		replyTo = strings.TrimSpace(message.Source.MessageID)
	}
	intent := channeltypes.DeliveryIntent{
		ID:        id,
		BindingID: message.Source.BindingID,
		TurnID:    message.TurnID,
		Sequence:  sequence,
		Status:    channeltypes.DeliveryPending,
		ChatID:    message.Source.ChatID,
		ReplyTo:   replyTo,
		ThreadID:  threadID,
	}
	if target := message.ReplyTarget; target != nil {
		intent.ResourceID = strings.TrimSpace(target.ResourceID)
		intent.ResourceType = strings.TrimSpace(target.ResourceType)
		intent.ParentID = strings.TrimSpace(target.ParentID)
		intent.TopLevel = target.TopLevel
	}
	return intent
}

func turnRecord(message channeltypes.InboundMessage, status channeltypes.TurnStatus) channeltypes.TurnRecord {
	return channeltypes.TurnRecord{
		TurnID:          message.TurnID,
		AgentID:         message.AgentID,
		BindingID:       message.Source.BindingID,
		ConversationKey: message.ConversationKey,
		Status:          status,
	}
}

func validateMessage(message channeltypes.InboundMessage) error {
	hasChat := strings.TrimSpace(message.Source.ChatID) != ""
	hasComment := isCommentReply(message) && strings.TrimSpace(message.ReplyTarget.ResourceID) != "" &&
		strings.TrimSpace(message.ReplyTarget.ResourceType) != "" && strings.TrimSpace(message.ReplyTarget.ParentID) != ""
	if strings.TrimSpace(message.AgentID) == "" || strings.TrimSpace(message.Source.BindingID) == "" ||
		strings.TrimSpace(message.Source.EventID) == "" || (!hasChat && !hasComment) ||
		strings.TrimSpace(message.ConversationKey) == "" || strings.TrimSpace(message.TurnID) == "" {
		return fmt.Errorf("feishu runner: agent, binding, source event, delivery target, conversation, and turn IDs are required")
	}
	return nil
}

func isCommentReply(message channeltypes.InboundMessage) bool {
	return message.ReplyTarget != nil && strings.TrimSpace(message.ReplyTarget.Kind) == channeltypes.ReplyTargetComment
}

func isChatReply(message channeltypes.InboundMessage) bool {
	return !isCommentReply(message)
}

func (r *Runner) current(key string) *activeRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active[strings.TrimSpace(key)]
}

// markEngineEntered makes the preflight-to-Engine ownership transfer atomic
// with Submit's decision to cancel an older preflight.
func (r *Runner) markEngineEntered(ctx context.Context, active *activeRun) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	active.engineEntered = true
	return true
}

func (r *Runner) ActiveTurn(key string) string {
	if active := r.current(key); active != nil {
		return active.turnID
	}
	return ""
}

// Wait blocks until all Runs owned by this binding runner have observed
// cancellation and completed their in-memory cleanup.
func (r *Runner) Wait(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		r.mu.Lock()
		active := make([]*activeRun, 0, len(r.runs))
		for turn := range r.runs {
			active = append(active, turn)
		}
		r.mu.Unlock()
		if len(active) == 0 {
			return nil
		}
		for _, turn := range active {
			turn.cancel()
			select {
			case <-turn.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (r *Runner) notify() {
	if r.notifier != nil {
		r.notifier.Notify()
	}
}

func (r *Runner) logFinalizeError(message channeltypes.InboundMessage, err error) {
	slog.Error("record terminal Feishu turn failed",
		messageLogAttrs(message, "error", err)...)
}

func (r *Runner) logTerminalResult(message channeltypes.InboundMessage, result agentengine.TurnResult) {
	attrs := messageLogAttrs(message, resultLogAttrs(result)...)
	switch result.Status {
	case agentengine.TurnSucceeded:
		slog.Debug("Feishu Agent Engine run completed", attrs...)
	case agentengine.TurnCanceled:
		slog.Debug("Feishu Agent Engine run canceled", attrs...)
	default:
		slog.Warn("Feishu Agent Engine run failed", attrs...)
	}
}

func (r *Runner) IsResetCommand(text string) bool {
	fields := strings.Fields(text)
	if len(fields) > 0 && strings.EqualFold(fields[0], "/new") {
		// Feishu renders the canonical command as slash text, so messages typed
		// in Feishu arrive as /new rather than the internal XML envelope.
		return true
	}
	command, ok, err := slashcommand.Parse(text)
	return err == nil && ok && slashcommand.IsNewConversationCommand(command)
}

func resultFromError(err error) agentengine.TurnResult {
	if errors.Is(err, context.Canceled) {
		return agentengine.TurnResult{
			Status: agentengine.TurnCanceled,
			Error:  &agentengine.TurnError{Code: agentengine.ErrorCanceled, Message: "Feishu turn was canceled"},
		}
	}
	var turnErr *agentengine.TurnError
	if errors.As(err, &turnErr) {
		status := agentengine.TurnFailed
		if turnErr.Code == agentengine.ErrorCanceled {
			status = agentengine.TurnCanceled
		}
		return agentengine.TurnResult{Status: status, Error: turnErr}
	}
	return agentengine.TurnResult{Status: agentengine.TurnFailed, Error: &agentengine.TurnError{Code: agentengine.ErrorRuntimeFailed, Message: err.Error()}}
}

func userFacingError(turnErr *agentengine.TurnError) string {
	if turnErr == nil {
		return "Agent execution failed. Please try again."
	}
	switch turnErr.Code {
	case agentengine.ErrorAgentUnavailable:
		return "Agent is currently unavailable. Start it and try again."
	case agentengine.ErrorRuntimeAdapterUnavailable:
		return "This Agent runtime does not support direct Feishu execution yet."
	case agentengine.ErrorConversationBusy:
		return "This conversation is already processing another message. Please try again shortly."
	case agentengine.ErrorConversationNotResumable:
		return "This conversation can no longer be resumed. Use /new to start a fresh conversation."
	case agentengine.ErrorFileUnavailable:
		return "The Feishu attachment could not be made available to the Agent."
	case agentengine.ErrorInteractionUnsupported:
		return "This Feishu channel cannot answer the requested interaction yet."
	case agentengine.ErrorInvalidRequest:
		return "The Feishu message could not be processed."
	default:
		return "Agent execution failed. Please try again."
	}
}

func processingReactionID(turnID string) string {
	return strings.TrimSpace(turnID) + ":reaction:add"
}
