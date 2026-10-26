package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/namespaces"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

// Respond answers a pending session request with exactly one of result or
// rpcErr.
func (e *Engine) Respond(ctx context.Context, topic string, id int64, result json.RawMessage, rpcErr *jsonrpc.Error) error {
	if (result == nil) == (rpcErr == nil) {
		return fmt.Errorf("%w: exactly one of result and error is required", types.ErrInvalidParams)
	}
	var req *types.PendingRequest
	var s *store.SessionRecord
	err := e.store.View(ctx, func(tx *store.Tx) (err error) {
		if req, err = tx.Request(id); err != nil {
			return err
		}
		s, err = tx.Session(topic)
		return err
	})
	if err != nil {
		return err
	}
	if req == nil {
		return fmt.Errorf("request %d: %w", id, types.ErrNotFound)
	}
	if req.Topic != topic {
		return fmt.Errorf("%w: request %d belongs to topic %s", types.ErrInvalidParams, id, req.Topic)
	}
	if s == nil {
		_ = e.store.Update(ctx, func(tx *store.Tx) error { return tx.DeleteRequest(id) })
		return fmt.Errorf("session %s: %w", topic, types.ErrNotFound)
	}

	// A result tells the relay what the request did and the transaction
	// hashes it produced; an error carries nothing, as in the JS client.
	payload, o := resultPayload(id, result), optsRequestRes.with(tnvForResult(id, req, result))
	if rpcErr != nil {
		payload, o = errorPayload(id, rpcErr), optsRequestRes
	}
	if err := e.publish(ctx, topic, s.SymKey, payload, o); err != nil {
		return err
	}
	return e.store.Update(ctx, func(tx *store.Tx) error {
		if err := tx.DeleteRequest(id); err != nil {
			return err
		}
		return tx.SetHistoryResponse(id, payload)
	})
}

// Update replaces the session namespaces. It returns once the relay has
// accepted the update; wait on the result for the dApp's ack. The new
// namespaces apply locally at once and are rolled back only if the update
// never reached the relay.
func (e *Engine) Update(ctx context.Context, topic string, ns map[string]types.SessionNamespace) (*Acknowledgement, error) {
	s, err := e.loadSession(ctx, topic)
	if err != nil {
		return nil, err
	}
	if s.Self.PublicKey != s.Controller {
		return nil, types.ErrUnauthorized
	}
	ns = namespaces.NormalizeSession(ns)
	if err := namespaces.ValidateSession(ns, "update()"); err != nil {
		return nil, invalid(err)
	}
	if err := namespaces.Conforming(s.RequiredNamespaces, ns, "update()"); err != nil {
		return nil, invalid(err)
	}

	setNamespaces := func(to map[string]types.SessionNamespace) func(*store.Tx) error {
		return func(tx *store.Tx) error {
			cur, err := tx.Session(topic)
			if err != nil || cur == nil {
				return err
			}
			cur.Namespaces = to
			return tx.PutSession(*cur)
		}
	}
	sent, err := e.sendRequest(ctx, s, methodUpdate, updateParams{Namespaces: ns}, optsUpdateReq, setNamespaces(ns))
	if err != nil {
		_ = e.store.Update(context.Background(), setNamespaces(s.Namespaces))
		return nil, err
	}
	return &Acknowledgement{e: e, sent: sent}, nil
}

// Emit sends a session event. Events get no response.
func (e *Engine) Emit(ctx context.Context, topic, chainID, name string, data json.RawMessage) error {
	s, err := e.loadSession(ctx, topic)
	if err != nil {
		return err
	}
	if !namespaces.IsValidChainForSession(s.Namespaces, chainID) {
		return fmt.Errorf("%w: chainId %q is not in the session", types.ErrInvalidParams, chainID)
	}
	if !namespaces.IsValidEventForSession(s.Namespaces, chainID, name) {
		return fmt.Errorf("%w: event %q is not allowed on %s", types.ErrInvalidParams, name, chainID)
	}
	if data == nil {
		data = json.RawMessage("null")
	}
	id := jsonrpc.PayloadID()
	payload := requestPayload(id, methodEvent, eventParams{Event: sessionEvent{Name: name, Data: data}, ChainID: chainID})
	if err := e.store.Update(ctx, func(tx *store.Tx) error {
		_, err := tx.InsertHistory(e.outHistory(id, topic, methodEvent, payload))
		return err
	}); err != nil {
		return err
	}
	return e.publish(ctx, topic, s.SymKey, payload, optsEventReq.with(correlate(id)))
}
