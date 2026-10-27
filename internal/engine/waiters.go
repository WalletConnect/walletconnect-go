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
	topic  string
	method string // protocol method awaited, e.g. wc_sessionRequest
	inner  string // the session request's own method, or ""
	ch     chan waitResult
	span   *trace.Span // trace of the action awaiting the answer (log lines)
	answer *trace.Span // OpenTelemetry span from the request leaving to the peer's answer
	actx   context.Context
	start  time.Time
}

// addWaiter registers the answer to id as awaited. inner is the session
// request's own method, for session requests.
func (e *Engine) addWaiter(ctx context.Context, id int64, topic, method, inner string) chan waitResult {
	attrs := []any{"id", id, "topic", topic, "method", method}
	if inner != "" {
		attrs = append(attrs, "innerMethod", inner)
	}
	answer, actx := e.tr.Child(ctx, "wc.peer.answer", attrs...)
	ch := make(chan waitResult, 1)
	e.wmu.Lock()
	e.waiters[id] = &waiter{topic: topic, method: method, inner: inner, ch: ch, span: trace.From(ctx),
		answer: answer, actx: context.WithoutCancel(actx), start: time.Now()}
	e.wmu.Unlock()
	return ch
}

func (e *Engine) removeWaiter(id int64) *waiter {
	e.wmu.Lock()
	w := e.waiters[id]
	delete(e.waiters, id)
	e.wmu.Unlock()
	return w
}

// settle ends the waiter's answer span and records the wait.
func (e *Engine) settle(w *waiter, err error, outcome string, attrs ...any) {
	if w == nil {
		return
	}
	w.answer.End(err, append([]any{"outcome", outcome}, attrs...)...)
	e.tr.RecordRequest(w.actx, time.Since(w.start), w.method, w.inner, outcome)
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
	in := trace.From(ctx)
	attrs := []any{"id", id, "via", in.ID()}
	w.answer.Link(in)
	switch {
	case r.err != nil:
		attrs = append(attrs, "outcome", r.err)
		e.settle(w, r.err, "error", "via", in.ID())
	case r.rpcErr != nil:
		attrs = append(attrs, "outcome", "peer error", "code", r.rpcErr.Code, "message", r.rpcErr.Message)
		e.settle(w, r.rpcErr, "peer_error", "via", in.ID(), "code", r.rpcErr.Code)
	default:
		attrs = append(attrs, "outcome", "ok")
		e.settle(w, nil, "ok", "via", in.ID())
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
		e.settle(w, err, "cancelled", "reason", err)
		w.ch <- waitResult{err: err}
	}
}

func (e *Engine) failAllWaiters(err error) {
	e.wmu.Lock()
	ws := e.waiters
	e.waiters = map[int64]*waiter{}
	e.wmu.Unlock()
	for _, w := range ws {
		e.settle(w, err, "cancelled", "reason", err)
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
		if w := e.removeWaiter(sent.id); w != nil {
			e.tr.Timeout(w.actx, w.method)
			e.settle(w, types.ErrExpired, "timeout", "after", timeout)
		}
		sent.span.Step("gave up waiting for peer", "id", sent.id, "after", timeout)
		return waitResult{}, types.ErrExpired
	case <-ctx.Done():
		e.settle(e.removeWaiter(sent.id), ctx.Err(), "cancelled", "reason", ctx.Err())
		sent.span.Step("stopped waiting for peer", "id", sent.id, "reason", ctx.Err())
		return waitResult{}, ctx.Err()
	case <-e.done:
		return waitResult{}, types.ErrClosed
	}
}
