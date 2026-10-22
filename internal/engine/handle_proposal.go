package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/sdkerr"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

// onPropose is the wallet receiving a dApp's wc_sessionPropose on the
// pairing topic. A valid proposal is stored and emitted; an invalid one is
// auto-rejected (tag 1121).
func (e *Engine) onPropose(ctx context.Context, topic, symHex string, msg *jsonrpc.Message) error {
	var pairing *store.Pairing
	if err := e.store.View(ctx, func(tx *store.Tx) (err error) {
		pairing, err = tx.Pairing(topic)
		return err
	}); err != nil {
		return temporary(err)
	}
	if pairing == nil || pairing.Role != store.RoleWallet {
		return e.record(ctx, topic, msg, nil)
	}
	var p proposeParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return e.reject(ctx, topic, symHex, msg, sdkerr.New(sdkerr.MissingOrInvalid, "session proposal params"), optsProposeAutoReject)
	}
	if rpcErr := validateProposal(&p); rpcErr != nil {
		return e.reject(ctx, topic, symHex, msg, rpcErr, optsProposeAutoReject)
	}
	if p.ExpiryTimestamp == 0 {
		p.ExpiryTimestamp = e.now() + fiveMinutes
	}
	if p.ExpiryTimestamp < e.now() {
		return e.record(ctx, topic, msg, nil)
	}
	if p.RequiredNamespaces == nil {
		p.RequiredNamespaces = map[string]types.ProposalNamespace{}
	}
	if p.OptionalNamespaces == nil {
		p.OptionalNamespaces = map[string]types.ProposalNamespace{}
	}
	proposal := types.Proposal{
		ID: msg.ID, PairingTopic: topic, ExpiryTimestamp: p.ExpiryTimestamp, Relays: p.Relays, Proposer: p.Proposer,
		RequiredNamespaces: p.RequiredNamespaces, OptionalNamespaces: p.OptionalNamespaces,
		SessionProperties: p.SessionProperties, ScopedProperties: p.ScopedProperties,
	}
	if err := e.store.Update(ctx, func(tx *store.Tx) error {
		if err := tx.PutProposal(store.ProposalRecord{Proposal: proposal}); err != nil {
			return err
		}
		_, err := tx.InsertHistory(e.inHistory(msg, topic, nil))
		return err
	}); err != nil {
		return temporary(err)
	}
	e.requestRearm()
	e.emit(types.EventSessionProposal{Proposal: proposal, Verification: types.VerificationUnknown})
	return nil
}

// onProposeResponse is the dApp receiving the wallet's answer to its proposal.
// On approval it derives the session key, stores a pending session and
// subscribes to the session topic, where the settle request arrives next.
func (e *Engine) onProposeResponse(ctx context.Context, topic string, msg *jsonrpc.Message, raw json.RawMessage) error {
	var prop *store.ProposalRecord
	if err := e.store.View(ctx, func(tx *store.Tx) (err error) {
		prop, err = tx.Proposal(msg.ID)
		return err
	}); err != nil {
		return temporary(err)
	}
	if prop == nil || prop.SelfPrivateKey == "" {
		return nil
	}
	if msg.Error != nil {
		if err := e.store.Update(ctx, func(tx *store.Tx) error { return tx.ForgetPairing(topic, msg.ID) }); err != nil {
			return temporary(err)
		}
		e.unsubscribe(ctx, topic)
		e.resolve(ctx, msg.ID, waitResult{rpcErr: msg.Error})
		return nil
	}
	var res proposeResult
	if err := json.Unmarshal(msg.Result, &res); err != nil {
		return fmt.Errorf("proposal response: %w", err)
	}
	selfPriv, err := crypto.DecodeKey(prop.SelfPrivateKey)
	if err != nil {
		return err
	}
	peerPub, err := crypto.DecodeKey(res.ResponderPublicKey)
	if err != nil {
		return fmt.Errorf("responder public key: %w", err)
	}
	sym, err := crypto.DeriveSymKey(selfPriv, peerPub)
	if err != nil {
		return err
	}
	sessionTopic := crypto.Topic(sym)
	if err := e.store.Update(ctx, func(tx *store.Tx) error {
		if err := tx.PutPendingSession(store.PendingSession{
			SessionTopic: sessionTopic, SymKey: crypto.EncodeKey(sym), ProposalID: msg.ID,
			PairingTopic: topic, SelfPublicKey: prop.Proposer.PublicKey, CreatedAt: e.now(),
		}); err != nil {
			return err
		}
		return tx.SetHistoryResponse(msg.ID, raw)
	}); err != nil {
		return temporary(err)
	}
	e.relay.SubscribeAsync(ctx, sessionTopic)
	e.requestRearm()
	return nil
}

// onSettle is the dApp receiving wc_sessionSettle on the session topic. It
// turns the pending session into a session, drops the pairing, acks the
// wallet and resolves Connection.Wait.
func (e *Engine) onSettle(ctx context.Context, topic, symHex string, msg *jsonrpc.Message) error {
	var pending *store.PendingSession
	var prop *store.ProposalRecord
	if err := e.store.View(ctx, func(tx *store.Tx) (err error) {
		if pending, err = tx.PendingSession(topic); err != nil || pending == nil {
			return err
		}
		prop, err = tx.Proposal(pending.ProposalID)
		return err
	}); err != nil {
		return temporary(err)
	}
	if pending == nil {
		return e.record(ctx, topic, msg, nil)
	}
	var p settleParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return e.reject(ctx, topic, symHex, msg, sdkerr.New(sdkerr.MissingOrInvalid, "onSessionSettleRequest() params"), optsSettleRes)
	}
	if rpcErr := validateSettle(&p, e.now()); rpcErr != nil {
		return e.reject(ctx, topic, symHex, msg, rpcErr, optsSettleRes)
	}
	session := types.Session{
		Topic: topic, PairingTopic: pending.PairingTopic, Relay: p.Relay, Expiry: p.Expiry, Acknowledged: true,
		Controller: p.Controller.PublicKey, Namespaces: p.Namespaces,
		RequiredNamespaces: map[string]types.ProposalNamespace{}, OptionalNamespaces: map[string]types.ProposalNamespace{},
		SessionProperties: p.SessionProperties, ScopedProperties: p.ScopedProperties, SessionConfig: p.SessionConfig,
		Self: types.Participant{PublicKey: pending.SelfPublicKey, Metadata: e.meta},
		Peer: p.Controller,
	}
	if prop != nil {
		session.RequiredNamespaces = prop.RequiredNamespaces
		session.OptionalNamespaces = prop.OptionalNamespaces
	}
	response := resultPayload(msg.ID, true)
	if err := e.store.Update(ctx, func(tx *store.Tx) error {
		if err := tx.PutSession(store.SessionRecord{Session: session, SymKey: symHex}); err != nil {
			return err
		}
		if err := tx.DeletePendingSession(topic); err != nil {
			return err
		}
		if err := tx.ForgetPairing(pending.PairingTopic, pending.ProposalID); err != nil {
			return err
		}
		_, err := tx.InsertHistory(e.inHistory(msg, topic, response))
		return err
	}); err != nil {
		return temporary(err)
	}
	e.enqueue(ctx, topic, symHex, response, optsSettleRes)
	e.unsubscribe(ctx, pending.PairingTopic)
	e.requestRearm()
	e.emit(types.EventSessionConnect{Session: session})
	e.resolve(ctx, pending.ProposalID, waitResult{session: &session})
	return nil
}

// onSettleResponse is the wallet receiving the dApp's ack of the settlement.
func (e *Engine) onSettleResponse(ctx context.Context, topic string, msg *jsonrpc.Message, raw json.RawMessage) error {
	if msg.Error != nil {
		if err := e.store.Update(ctx, func(tx *store.Tx) error { return tx.DeleteSession(topic) }); err != nil {
			return temporary(err)
		}
		e.unsubscribe(ctx, topic)
		e.resolve(ctx, msg.ID, waitResult{rpcErr: msg.Error})
		return nil
	}
	if err := e.store.Update(ctx, func(tx *store.Tx) error {
		s, err := tx.Session(topic)
		if err != nil || s == nil {
			return err
		}
		s.Acknowledged = true
		if err := tx.PutSession(*s); err != nil {
			return err
		}
		return tx.SetHistoryResponse(msg.ID, raw)
	}); err != nil {
		return temporary(err)
	}
	e.resolve(ctx, msg.ID, waitResult{raw: msg.Result})
	return nil
}
