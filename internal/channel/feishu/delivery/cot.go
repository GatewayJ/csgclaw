package delivery

import (
	"context"
	"fmt"
	"time"

	channel "csgclaw/internal/channel"
	"csgclaw/internal/channel/feishu/presentation"
	"csgclaw/internal/channel/feishu/transport"
)

func isCOT(kind channel.DeliveryKind) bool {
	return kind == channel.DeliveryCOTCreate || kind == channel.DeliveryCOTUpdate || kind == channel.DeliveryCOTComplete
}

// COT has its own worker so a process API request cannot delay an answer or
// a permission card. Each batch is attempted once: the API has no replay key.
func (d *Dispatcher) runCOT(ctx context.Context) {
	defer close(d.cotDone)
	ticker := time.NewTicker(600 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.drainCOT(ctx)
		case <-d.cotWake:
			d.drainCOTPending(ctx, true)
		}
	}
}

func (d *Dispatcher) drainCOT(ctx context.Context) { d.drainCOTPending(ctx, false) }
func (d *Dispatcher) drainCOTPending(ctx context.Context, createsOnly bool) {
	pending := d.state.Pending()
	client, ok := d.adapter.(transport.COTAdapter)
	failed := map[string]bool{}
	for _, item := range pending {
		if isCOT(item.Kind) {
			failed[item.TurnID] = d.state.COTFailed(item.TurnID)
		}
	}
	for index := 0; index < len(pending); index++ {
		intent := pending[index]
		if !isCOT(intent.Kind) || (createsOnly && intent.Kind != channel.DeliveryCOTCreate) {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		batch := []channel.DeliveryIntent{intent}
		if intent.Kind == channel.DeliveryCOTUpdate {
			size := 0
			for _, e := range intent.Events {
				size += len(e.Content)
			}
			for index+1 < len(pending) {
				next := pending[index+1]
				if next.Kind != channel.DeliveryCOTUpdate || next.TurnID != intent.TurnID {
					break
				}
				n := 0
				for _, e := range next.Events {
					n += len(e.Content)
				}
				if size+n > 16000 {
					break
				}
				size += n
				index++
				batch = append(batch, next)
				intent.Events = append(intent.Events, next.Events...)
			}
		}
		var err error
		if !ok {
			err = fmt.Errorf("COT API is unavailable")
		}
		var ref transport.COTRef
		if intent.Kind != channel.DeliveryCOTCreate && err == nil {
			create, found := d.state.Intent(intent.RelatedID)
			if !found || create.Status == channel.DeliveryFailed {
				err = ErrDependencyTerminal
			} else if create.Status != channel.DeliveryDelivered {
				continue
			} else {
				ref = transport.COTRef{COTID: create.COTID, MessageID: create.MessageID}
			}
		}
		for _, item := range batch {
			_ = d.state.Begin(item.ID)
		}
		requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err == nil {
			switch intent.Kind {
			case channel.DeliveryCOTCreate:
				ref, err = client.CreateCOT(requestCtx, transport.COTCreateRequest{ChatID: intent.ChatID, OriginMessageID: intent.OriginMessageID})
			case channel.DeliveryCOTUpdate:
				if failed[intent.TurnID] {
					err = ErrDependencyTerminal
				} else {
					err = client.UpdateCOT(requestCtx, transport.COTUpdateRequest{Ref: ref, Events: intent.Events})
				}
			case channel.DeliveryCOTComplete:
				if !failed[intent.TurnID] {
					err = client.UpdateCOT(requestCtx, transport.COTUpdateRequest{Ref: ref, Events: intent.Events})
				}
				cancel()
				requestCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
				completeErr := client.CompleteCOT(requestCtx, transport.COTCompleteRequest{Ref: ref, Reason: intent.Reason})
				if err == nil {
					err = completeErr
				}
			}
		}
		cancel()
		if intent.Kind == channel.DeliveryCOTComplete {
			d.state.Pin(intent.RelatedID, false)
		}
		for _, item := range batch {
			if err != nil {
				_ = d.state.MarkFailed(item.ID, err)
			} else {
				item.COTID = ref.COTID
				item.MessageID = ref.MessageID
				_ = d.state.MarkDelivered(item)
			}
		}
		if err != nil {
			failed[intent.TurnID] = true
			d.logDeliveryFailure(intent, err, false, time.Time{})
			notice := intent
			notice.ID = intent.TurnID + ":cot:unavailable"
			notice.Kind = channel.DeliveryCard
			notice.RelatedID = ""
			notice.Events = nil
			notice.Card = presentation.Card("过程展示暂时不可用，执行结果将通过回复卡片发送。")
			_ = d.state.Enqueue(notice)
			d.Notify()
		}
	}
}
