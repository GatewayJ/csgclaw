package execution

import (
	"context"
	"fmt"

	channel "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/interaction"
	feishustate "csgclaw/internal/channel/feishu/state"
)

type conversationControl struct {
	gate chan struct{}
	refs int
}

// acquireControl serializes admission, reset and answers within a conversation.
// References include waiting callers so an entry survives until every caller
// releases it. Engine calls never hold the map mutex.
func (r *Runner) acquireControl(ctx context.Context, key string) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.controlMu.Lock()
	control := r.controls[key]
	if control == nil {
		control = &conversationControl{gate: make(chan struct{}, 1)}
		r.controls[key] = control
	}
	control.refs++
	r.controlMu.Unlock()

	releaseRef := func() {
		r.controlMu.Lock()
		control.refs--
		if control.refs == 0 {
			delete(r.controls, key)
		}
		r.controlMu.Unlock()
	}
	select {
	case control.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-control.gate
			releaseRef()
			return nil, err
		}
		return func() {
			<-control.gate
			releaseRef()
		}, nil
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	}
}

// CancelRequest keeps task cancellation independent from process presentation.
func (r *Runner) CancelRequest(ctx context.Context, request interaction.CancelRequest) error {
	release, err := r.acquireControl(ctx, request.ConversationKey)
	if err != nil {
		return err
	}
	defer release()
	target, found := r.state.ResolveControlTarget(feishustate.ControlQuery{BindingID: request.BindingID, AgentID: request.AgentID, MessageID: request.MessageID, ChatID: request.ChatID, ThreadID: request.ThreadID, RequesterID: request.RequesterID})
	if !found || request.RequesterID == "" || target.Intent.RequesterID == "" || target.Turn.TurnID != request.TurnID || target.Turn.ConversationKey != request.ConversationKey {
		return fmt.Errorf("无法确认此操作对应的任务或操作权限")
	}
	message := channel.InboundMessage{AgentID: request.AgentID, TurnID: request.TurnID, ConversationKey: request.ConversationKey, Source: channel.Source{BindingID: request.BindingID, MessageID: request.MessageID, ChatID: request.ChatID}}
	switch target.Turn.Status {
	case channel.TurnSucceeded, channel.TurnFailed, channel.TurnCanceled:
	default:
		if r.ActiveTurn(request.ConversationKey) != request.TurnID {
			return fmt.Errorf("此任务已不再是当前执行的任务")
		}
		r.state.MarkCanceling(request.TurnID)
		r.notify()
		if err := r.Cancel(ctx, request.AgentID, request.ConversationKey, request.TurnID); err != nil {
			return err
		}
	}
	// Completion delivery owns retries and its failure must not report that task
	// cancellation failed. A successful completion is left unchanged.
	if err := r.state.RetryCOTCompletion(request.TurnID + ":cot:create"); err != nil {
		r.logFinalizeError(message, err)
	}
	r.notify()
	return nil
}
