package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/store"
)

func marshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("engine: marshal: %v", err))
	}
	return b
}

func requestPayload(id int64, method string, params any) json.RawMessage {
	return marshal(jsonrpc.Request{ID: id, JSONRPC: jsonrpc.Version, Method: method, Params: params})
}

func resultPayload(id int64, result any) json.RawMessage {
	return marshal(jsonrpc.Result{ID: id, JSONRPC: jsonrpc.Version, Result: result})
}

func errorPayload(id int64, err *jsonrpc.Error) json.RawMessage {
	return marshal(jsonrpc.ErrorResponse{ID: id, JSONRPC: jsonrpc.Version, Error: err})
}

func encrypt(symHex string, payload json.RawMessage) (string, error) {
	sym, err := crypto.DecodeKey(symHex)
	if err != nil {
		return "", err
	}
	return crypto.Encrypt(sym, payload)
}

func publishParams(topic, message string, o publishOpts) map[string]any {
	p := map[string]any{"topic": topic, "message": message, "ttl": o.ttl, "tag": o.tag}
	if o.prompt {
		p["prompt"] = true
	}
	o.tvf.addTo(p)
	return p
}

// publish encrypts and sends a payload, waiting for the relay to accept it.
func (e *Engine) publish(ctx context.Context, topic, symHex string, payload json.RawMessage, o publishOpts) error {
	msg, err := encrypt(symHex, payload)
	if err != nil {
		return err
	}
	_, err = e.relay.Request(ctx, "irn_publish", publishParams(topic, msg, o))
	return err
}

// enqueue encrypts and sends a payload without waiting. Used by handlers;
// ctx carries the trace of the message being answered.
func (e *Engine) enqueue(ctx context.Context, topic, symHex string, payload json.RawMessage, o publishOpts) {
	msg, err := encrypt(symHex, payload)
	if err != nil {
		e.log.Error("encrypt response", "topic", topic, "err", err)
		return
	}
	e.relay.Enqueue(ctx, "irn_publish", publishParams(topic, msg, o), false)
}

func (e *Engine) unsubscribe(ctx context.Context, topic string) {
	e.relay.Unsubscribe(ctx, topic)
}

func (e *Engine) outHistory(id int64, topic, method string, payload json.RawMessage) store.History {
	return store.History{ID: id, Topic: topic, Method: method, Request: payload, Direction: store.DirectionOut, CreatedAt: e.now()}
}

func (e *Engine) inHistory(msg *jsonrpc.Message, topic string, response json.RawMessage) store.History {
	return store.History{ID: msg.ID, Topic: topic, Method: msg.Method, Request: msg.Params, Response: response,
		Direction: store.DirectionIn, CreatedAt: e.now()}
}
