package execution

import "context"

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
