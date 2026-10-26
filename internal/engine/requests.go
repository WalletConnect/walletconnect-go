package engine

import (
	"context"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/trace"
)

// sentRequest is an outgoing session request whose response is awaited.
type sentRequest struct {
	id    int64
	reply chan waitResult
	span  *trace.Span
}

// sendRequest records a request in history, applies change in the same
// transaction, and publishes it on the session topic. An error means the
// peer never received the request.
func (e *Engine) sendRequest(ctx context.Context, s *store.SessionRecord, method string, params any, o publishOpts, change func(*store.Tx) error) (sentRequest, error) {
	id := jsonrpc.PayloadID()
	payload := requestPayload(id, method, params)
	err := e.store.Update(ctx, func(tx *store.Tx) error {
		if change != nil {
			if err := change(tx); err != nil {
				return err
			}
		}
		_, err := tx.InsertHistory(e.outHistory(id, s.Topic, method, payload))
		return err
	})
	if err != nil {
		return sentRequest{}, err
	}
	inner := ""
	t := correlate(id)
	if rp, ok := params.(requestParams); ok {
		inner = rp.Request.Method
		t = tnvForRequest(id, rp)
	}
	o = o.with(t)
	reply := e.addWaiter(ctx, id, s.Topic, method, inner)
	if err := e.publish(ctx, s.Topic, s.SymKey, payload, o); err != nil {
		e.settle(e.removeWaiter(id), err, "publish_failed")
		return sentRequest{}, err
	}
	trace.From(ctx).Step("request published", "id", id, "method", method)
	return sentRequest{id: id, reply: reply, span: trace.From(ctx)}, nil
}

// Acknowledgement is a request the relay has accepted and the peer has yet to
// answer. Waiting on it is optional.
type Acknowledgement struct {
	e    *Engine
	sent sentRequest
}

// Wait blocks until the peer acknowledges, rejects (returned as
// *jsonrpc.Error), or 5 minutes pass (ErrExpired).
func (a *Acknowledgement) Wait(ctx context.Context) error {
	_, err := a.e.await(ctx, a.sent, ackTimeout)
	return err
}
