package engine

import (
	"context"
	"encoding/json"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/trace"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

type waitResult struct {
	raw     json.RawMessage
	rpcErr  *jsonrpc.Error
	err     error
	session *types.Session
}

type waiter struct {
	topic string
	ch    chan waitResult
	span  *trace.Span // trace of the action awaiting the answer
}

func (e *Engine) addWaiter(ctx context.Context, id int64, topic string) chan waitResult {
	ch := make(chan waitResult, 1)
	e.wmu.Lock()
	e.waiters[id] = &waiter{topic: topic, ch: ch, span: trace.From(ctx)}
	e.wmu.Unlock()
	return ch
}

func (e *Engine) removeWaiter(id int64) {
	e.wmu.Lock()
	delete(e.waiters, id)
	e.wmu.Unlock()
}

// resolve hands a result to the action awaiting id. ctx carries the trace of
// the incoming message that produced it, which the action's trace links to.
func (e *Engine) resolve(ctx context.Context, id int64, r waitResult) {
	e.wmu.Lock()
	w := e.waiters[id]
	delete(e.waiters, id)
	e.wmu.Unlock()
	if w == nil {
		return
	}
	attrs := []any{"id", id, "via", trace.From(ctx).ID()}
	switch {
	case r.err != nil:
		attrs = append(attrs, "outcome", r.err)
	case r.rpcErr != nil:
		attrs = append(attrs, "outcome", "peer error", "code", r.rpcErr.Code, "message", r.rpcErr.Message)
	default:
		attrs = append(attrs, "outcome", "ok")
	}
	w.span.Step("peer answered", attrs...)
	w.ch <- r
}

func (e *Engine) failTopic(topic string, err error) {
	e.wmu.Lock()
	var hit []*waiter
	for id, w := range e.waiters {
		if w.topic == topic {
			hit = append(hit, w)
			delete(e.waiters, id)
		}
	}
	e.wmu.Unlock()
	for _, w := range hit {
		w.span.Step("wait cancelled", "reason", err)
		w.ch <- waitResult{err: err}
	}
}

func (e *Engine) failAllWaiters(err error) {
	e.wmu.Lock()
	ws := e.waiters
	e.waiters = map[int64]*waiter{}
	e.wmu.Unlock()
	for _, w := range ws {
		w.ch <- waitResult{err: err}
	}
}

// await waits for the reply to a sent request, the timeout, ctx or engine
// shutdown. A peer error is returned as *jsonrpc.Error.
func (e *Engine) await(ctx context.Context, sent sentRequest, timeout time.Duration) (waitResult, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-sent.reply:
		if r.err != nil {
			return r, r.err
		}
		if r.rpcErr != nil {
			return r, r.rpcErr
		}
		return r, nil
	case <-timer.C:
		e.removeWaiter(sent.id)
		sent.span.Step("gave up waiting for peer", "id", sent.id, "after", timeout)
		return waitResult{}, types.ErrExpired
	case <-ctx.Done():
		e.removeWaiter(sent.id)
		sent.span.Step("stopped waiting for peer", "id", sent.id, "reason", ctx.Err())
		return waitResult{}, ctx.Err()
	case <-e.done:
		return waitResult{}, types.ErrClosed
	}
}
