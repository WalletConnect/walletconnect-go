package engine

import (
	"context"
	"encoding/json"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/namespaces"
	"github.com/WalletConnect/walletconnect-go/internal/sdkerr"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

// onRequest is the wallet receiving a wc_sessionRequest. A valid request is
// stored as pending and emitted; the app answers it with Respond.
func (e *Engine) onRequest(ctx context.Context, topic, symHex string, msg *jsonrpc.Message) error {
	s, err := e.sessionFor(ctx, topic)
	if err != nil {
		return err
	}
	if s == nil {
		return e.reject(ctx, topic, symHex, msg, noSession(topic), optsRequestRes)
	}
	var p requestParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return e.reject(ctx, topic, symHex, msg, sdkerr.New(sdkerr.MissingOrInvalid, "request() params"), optsRequestRes)
	}
	if !namespaces.IsValidChainForSession(s.Namespaces, p.ChainID) {
		return e.reject(ctx, topic, symHex, msg, sdkerr.New(sdkerr.MissingOrInvalid, "request() chainId: "+p.ChainID), optsRequestRes)
	}
	if p.Request.Method == "" {
		return e.reject(ctx, topic, symHex, msg, sdkerr.New(sdkerr.MissingOrInvalid, "request() method"), optsRequestRes)
	}
	if !namespaces.IsValidMethodForSession(s.Namespaces, p.ChainID, p.Request.Method) {
		return e.reject(ctx, topic, symHex, msg, sdkerr.New(sdkerr.MissingOrInvalid, "request() method: "+p.Request.Method), optsRequestRes)
	}
	expiry := p.Request.ExpiryTimestamp
	if expiry == 0 {
		expiry = e.now() + requestTTLDefault
	}
	if expiry < e.now() {
		return e.record(ctx, topic, msg, nil)
	}
	req := types.PendingRequest{ID: msg.ID, Topic: topic, ChainID: p.ChainID, Method: p.Request.Method,
		Params: p.Request.Params, ExpiryTimestamp: expiry}
	if err := e.store.Update(ctx, func(tx *store.Tx) error {
		if err := tx.PutRequest(req); err != nil {
			return err
		}
		_, err := tx.InsertHistory(e.inHistory(msg, topic, nil))
		return err
	}); err != nil {
		return temporary(err)
	}
	e.requestRearm()
	e.emit(types.EventSessionRequest{Topic: topic, ID: msg.ID, ChainID: p.ChainID, Method: req.Method,
		Params: req.Params, ExpiryTimestamp: expiry, Verification: types.VerificationUnknown})
	return nil
}

// onEvent is the dApp receiving a wc_sessionEvent. Events older than the last
// one of the same name are dropped. Valid events get no response, like JS.
func (e *Engine) onEvent(ctx context.Context, topic, symHex string, msg *jsonrpc.Message) error {
	s, err := e.sessionFor(ctx, topic)
	if err != nil {
		return err
	}
	if s == nil {
		return e.reject(ctx, topic, symHex, msg, noSession(topic), optsEventRes)
	}
	var p eventParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return e.reject(ctx, topic, symHex, msg, sdkerr.New(sdkerr.MissingOrInvalid, "emit() params"), optsEventRes)
	}
	key := topic + "/" + p.Event.Name
	if !e.inOrder(e.lastEvent, key, msg.ID) {
		return e.record(ctx, topic, msg, nil)
	}
	if !namespaces.IsValidChainForSession(s.Namespaces, p.ChainID) {
		return e.reject(ctx, topic, symHex, msg, sdkerr.New(sdkerr.MissingOrInvalid, "emit() chainId: "+p.ChainID), optsEventRes)
	}
	if !namespaces.IsValidEventForSession(s.Namespaces, p.ChainID, p.Event.Name) {
		return e.reject(ctx, topic, symHex, msg, sdkerr.New(sdkerr.MissingOrInvalid, "emit() event: "+p.Event.Name), optsEventRes)
	}
	if err := e.record(ctx, topic, msg, nil); err != nil {
		return err
	}
	e.markSeen(e.lastEvent, key, msg.ID)
	e.emit(types.EventSessionEvent{Topic: topic, ID: msg.ID, ChainID: p.ChainID, Name: p.Event.Name, Data: p.Event.Data})
	return nil
}

// onUpdate is the dApp receiving new namespaces from the wallet. Only the
// controller may update, and updates older than the last applied one are
// rejected.
func (e *Engine) onUpdate(ctx context.Context, topic, symHex string, msg *jsonrpc.Message) error {
	s, err := e.sessionFor(ctx, topic)
	if err != nil {
		return err
	}
	if s == nil {
		return e.reject(ctx, topic, symHex, msg, noSession(topic), optsUpdateRes)
	}
	if !e.inOrder(e.lastUpdate, topic, msg.ID) {
		return e.reject(ctx, topic, symHex, msg, sdkerr.New(sdkerr.InvalidUpdateRequest, ""), optsUpdateRes)
	}
	if s.Peer.PublicKey != s.Controller {
		return e.reject(ctx, topic, symHex, msg, sdkerr.New(sdkerr.UnauthorizedUpdateRequest, ""), optsUpdateRes)
	}
	var p updateParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return e.reject(ctx, topic, symHex, msg, sdkerr.New(sdkerr.MissingOrInvalid, "update() params"), optsUpdateRes)
	}
	if rpcErr := namespaces.ValidateSession(p.Namespaces, "update()"); rpcErr != nil {
		return e.reject(ctx, topic, symHex, msg, rpcErr, optsUpdateRes)
	}
	if rpcErr := namespaces.Conforming(s.RequiredNamespaces, p.Namespaces, "update()"); rpcErr != nil {
		return e.reject(ctx, topic, symHex, msg, rpcErr, optsUpdateRes)
	}
	response := resultPayload(msg.ID, true)
	s.Namespaces = p.Namespaces
	if err := e.store.Update(ctx, func(tx *store.Tx) error {
		if err := tx.PutSession(*s); err != nil {
			return err
		}
		_, err := tx.InsertHistory(e.inHistory(msg, topic, response))
		return err
	}); err != nil {
		return temporary(err)
	}
	e.markSeen(e.lastUpdate, topic, msg.ID)
	e.enqueue(ctx, topic, symHex, response, optsUpdateRes.with(correlate(msg.ID)))
	e.emit(types.EventSessionUpdate{Topic: topic, ID: msg.ID, Namespaces: p.Namespaces})
	return nil
}

// onExtend moves the session expiry 7 days out. Any peer may extend, like JS.
func (e *Engine) onExtend(ctx context.Context, topic, symHex string, msg *jsonrpc.Message) error {
	s, err := e.sessionFor(ctx, topic)
	if err != nil {
		return err
	}
	if s == nil {
		return e.reject(ctx, topic, symHex, msg, noSession(topic), optsExtendRes)
	}
	s.Expiry = e.now() + sevenDays
	response := resultPayload(msg.ID, true)
	if err := e.store.Update(ctx, func(tx *store.Tx) error {
		if err := tx.PutSession(*s); err != nil {
			return err
		}
		_, err := tx.InsertHistory(e.inHistory(msg, topic, response))
		return err
	}); err != nil {
		return temporary(err)
	}
	e.enqueue(ctx, topic, symHex, response, optsExtendRes.with(correlate(msg.ID)))
	e.requestRearm()
	e.emit(types.EventSessionExtend{Topic: topic, ID: msg.ID, Expiry: s.Expiry})
	return nil
}

// onPing answers a session ping.
func (e *Engine) onPing(ctx context.Context, topic, symHex string, msg *jsonrpc.Message) error {
	s, err := e.sessionFor(ctx, topic)
	if err != nil {
		return err
	}
	if s == nil {
		return e.reject(ctx, topic, symHex, msg, noSession(topic), optsPingRes)
	}
	response := resultPayload(msg.ID, true)
	if err := e.record(ctx, topic, msg, response); err != nil {
		return err
	}
	e.enqueue(ctx, topic, symHex, response, optsPingRes.with(correlate(msg.ID)))
	e.emit(types.EventSessionPing{Topic: topic, ID: msg.ID})
	return nil
}

// onDelete removes a session the peer disconnected. JS sends no response.
func (e *Engine) onDelete(ctx context.Context, topic string, msg *jsonrpc.Message) error {
	s, err := e.sessionFor(ctx, topic)
	if err != nil {
		return err
	}
	if s == nil {
		return e.record(ctx, topic, msg, nil)
	}
	var reason jsonrpc.Error
	_ = json.Unmarshal(msg.Params, &reason)
	if err := e.store.Update(ctx, func(tx *store.Tx) error { return tx.DeleteSession(topic) }); err != nil {
		return temporary(err)
	}
	e.forgetTopic(topic)
	e.unsubscribe(ctx, topic)
	e.failTopic(topic, types.ErrSessionDeleted)
	e.emit(types.EventSessionDelete{Topic: topic, ID: msg.ID, Reason: &reason})
	return nil
}
