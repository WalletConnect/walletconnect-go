package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/relay"
	"github.com/WalletConnect/walletconnect-go/internal/sdkerr"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/trace"
)

var errTemporary = errors.New("engine: temporary failure")

// temporary marks err as a storage failure, so the message is not acked and
// the relay redelivers it.
func temporary(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", errTemporary, err)
}

// handle processes one incoming message and reports whether to ack it.
// Only storage failures leave a message unacked, so the relay redelivers it.
func (e *Engine) handle(ctx context.Context, in relay.Incoming) bool {
	symHex, msg, err := e.open(ctx, in)
	if err != nil {
		if errors.Is(err, errTemporary) {
			e.log.Error("open message", "topic", in.Topic, "err", err)
			in.Span.End(err, "acked", false, "reason", "storage failed; the relay redelivers it")
			return false
		}
		e.log.Warn("dropping message", "topic", in.Topic, "err", err)
		in.Span.Step("dropped", "reason", err)
		return true
	}
	kind := msg.Method
	if !msg.IsRequest() {
		kind = "response"
	}
	in.Span.Step("decrypted", "id", msg.ID, "kind", kind)
	if msg.IsRequest() {
		err = e.handleRequest(ctx, in.Topic, symHex, msg)
	} else {
		err = e.handleResponse(ctx, in.Topic, msg)
	}
	if err != nil {
		e.log.Error("handle message", "topic", in.Topic, "id", msg.ID, "method", msg.Method, "err", err)
		if errors.Is(err, errTemporary) {
			in.Span.End(err, "acked", false, "reason", "storage failed; the relay redelivers it")
			return false
		}
		in.Span.Step("handler failed", "err", err)
		return true
	}
	in.Span.Step("handled")
	return true
}

var errUnknownTopic = errors.New("no key for topic")

// open finds the topic's key, decrypts the envelope and parses the JSON-RPC
// payload.
func (e *Engine) open(ctx context.Context, in relay.Incoming) (string, *jsonrpc.Message, error) {
	var symHex string
	if err := e.store.View(ctx, func(tx *store.Tx) (err error) {
		symHex, err = tx.SymKey(in.Topic)
		return err
	}); err != nil {
		return "", nil, temporary(err)
	}
	if symHex == "" {
		return "", nil, errUnknownTopic
	}
	sym, err := crypto.DecodeKey(symHex)
	if err != nil {
		return "", nil, fmt.Errorf("stored key: %w", err)
	}
	plain, err := crypto.Decrypt(sym, in.Message)
	if err != nil {
		return "", nil, err
	}
	msg, err := jsonrpc.Parse(plain)
	if err != nil {
		return "", nil, err
	}
	return symHex, msg, nil
}

// handleRequest drops requests already in history and routes the rest by method.
func (e *Engine) handleRequest(ctx context.Context, topic, symHex string, msg *jsonrpc.Message) error {
	var seen bool
	if err := e.store.View(ctx, func(tx *store.Tx) error {
		h, err := tx.History(msg.ID)
		seen = h != nil
		return err
	}); err != nil {
		return temporary(err)
	}
	if seen {
		e.log.Debug("dropping duplicate request", "id", msg.ID, "method", msg.Method)
		trace.From(ctx).Step("duplicate, already handled", "id", msg.ID)
		return nil
	}
	switch msg.Method {
	case methodPropose:
		return e.onPropose(ctx, topic, symHex, msg)
	case methodSettle:
		return e.onSettle(ctx, topic, symHex, msg)
	case methodUpdate:
		return e.onUpdate(ctx, topic, symHex, msg)
	case methodExtend:
		return e.onExtend(ctx, topic, symHex, msg)
	case methodPing:
		return e.onPing(ctx, topic, symHex, msg)
	case methodDelete:
		return e.onDelete(ctx, topic, msg)
	case methodRequest:
		return e.onRequest(ctx, topic, symHex, msg)
	case methodEvent:
		return e.onEvent(ctx, topic, symHex, msg)
	default:
		// wc_sessionAuthenticate lands here too: the dApp's fallback
		// wc_sessionPropose drives the session instead.
		if msg.Method != methodAuthenticate {
			e.log.Info("ignoring unsupported method", "method", msg.Method)
		}
		return e.record(ctx, topic, msg, nil)
	}
}

// handleResponse routes a response by the method of the request it answers,
// found in history. Unknown and already-answered responses are dropped.
func (e *Engine) handleResponse(ctx context.Context, topic string, msg *jsonrpc.Message) error {
	var h *store.History
	if err := e.store.View(ctx, func(tx *store.Tx) (err error) {
		h, err = tx.History(msg.ID)
		return err
	}); err != nil {
		return temporary(err)
	}
	if h == nil || h.Direction != store.DirectionOut || h.Response != nil {
		e.log.Debug("dropping unknown or duplicate response", "id", msg.ID)
		trace.From(ctx).Step("unknown or duplicate response", "id", msg.ID)
		return nil
	}
	raw := resultPayload(msg.ID, msg.Result)
	if msg.Error != nil {
		raw = errorPayload(msg.ID, msg.Error)
	}
	switch h.Method {
	case methodPropose:
		return e.onProposeResponse(ctx, topic, msg, raw)
	case methodSettle:
		return e.onSettleResponse(ctx, topic, msg, raw)
	default:
		if err := e.store.Update(ctx, func(tx *store.Tx) error { return tx.SetHistoryResponse(msg.ID, raw) }); err != nil {
			return temporary(err)
		}
		e.resolve(ctx, msg.ID, waitResult{raw: msg.Result, rpcErr: msg.Error})
		return nil
	}
}

// record stores the incoming request in history with an optional response.
func (e *Engine) record(ctx context.Context, topic string, msg *jsonrpc.Message, response json.RawMessage) error {
	return temporary(e.store.Update(ctx, func(tx *store.Tx) error {
		_, err := tx.InsertHistory(e.inHistory(msg, topic, response))
		return err
	}))
}

// reject records the request and answers it with an error.
func (e *Engine) reject(ctx context.Context, topic, symHex string, msg *jsonrpc.Message, rpcErr *jsonrpc.Error, o publishOpts) error {
	payload := errorPayload(msg.ID, rpcErr)
	if err := e.record(ctx, topic, msg, payload); err != nil {
		return err
	}
	e.log.Warn("rejecting request", "method", msg.Method, "id", msg.ID, "code", rpcErr.Code, "message", rpcErr.Message)
	e.enqueue(ctx, topic, symHex, payload, o)
	return nil
}

// sessionFor loads a session for a handler; storage errors are temporary.
func (e *Engine) sessionFor(ctx context.Context, topic string) (*store.SessionRecord, error) {
	var s *store.SessionRecord
	err := e.store.View(ctx, func(tx *store.Tx) (err error) {
		s, err = tx.Session(topic)
		return err
	})
	return s, temporary(err)
}

func noSession(topic string) *jsonrpc.Error {
	return sdkerr.New(sdkerr.NoMatchingKey, fmt.Sprintf("session topic doesn't exist: %s", topic))
}
