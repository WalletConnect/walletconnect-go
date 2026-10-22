package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/trace"
	"github.com/WalletConnect/walletconnect-go/internal/types"
	"github.com/WalletConnect/walletconnect-go/internal/uri"
)

type ConnectParams struct {
	OptionalNamespaces map[string]types.ProposalNamespace
	SessionProperties  map[string]string
	ScopedProperties   map[string]json.RawMessage
}

// Connection is a sent proposal awaiting the wallet's answer.
type Connection struct {
	URI          string
	PairingTopic string
	ProposalID   int64
	Expiry       int64

	e        *Engine
	proposal sentRequest
}

// Wait blocks until the wallet settles or rejects the session, or the
// proposal expires.
func (c *Connection) Wait(ctx context.Context) (types.Session, error) {
	timeout := time.Unix(c.Expiry, 0).Sub(c.e.clock()) + 5*time.Second
	r, err := c.e.await(ctx, c.proposal, timeout)
	if err != nil {
		return types.Session{}, err
	}
	return *r.session, nil
}

// Connect creates a pairing, subscribes to it and sends the proposal through
// wc_proposeSession. The wallet's answer resolves Connection.Wait.
func (e *Engine) Connect(ctx context.Context, p ConnectParams) (*Connection, error) {
	optional, err := validateConnect(p)
	if err != nil {
		return nil, err
	}
	pairingKey, err := crypto.RandomKey()
	if err != nil {
		return nil, err
	}
	self, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	pairingTopic := crypto.Topic(pairingKey)
	symHex := crypto.EncodeKey(pairingKey)
	proposal := types.Proposal{
		ID:                 jsonrpc.PayloadID(),
		PairingTopic:       pairingTopic,
		ExpiryTimestamp:    e.now() + fiveMinutes,
		Relays:             []types.Relay{{Protocol: relayProtocol}},
		Proposer:           types.Participant{PublicKey: crypto.EncodeKey(self.Public), Metadata: e.meta},
		RequiredNamespaces: map[string]types.ProposalNamespace{},
		OptionalNamespaces: optional,
		SessionProperties:  p.SessionProperties,
		ScopedProperties:   p.ScopedProperties,
	}
	payload := requestPayload(proposal.ID, methodPropose, proposeParams{
		RequiredNamespaces: proposal.RequiredNamespaces, OptionalNamespaces: optional, Relays: proposal.Relays,
		Proposer: proposal.Proposer, ExpiryTimestamp: proposal.ExpiryTimestamp, PairingTopic: pairingTopic,
		ID: proposal.ID, SessionProperties: p.SessionProperties, ScopedProperties: p.ScopedProperties,
	})

	err = e.store.Update(ctx, func(tx *store.Tx) error {
		pairing := store.Pairing{Topic: pairingTopic, SymKey: symHex, Expiry: proposal.ExpiryTimestamp, Role: store.RoleDApp, ProposalID: proposal.ID}
		if err := tx.PutPairing(pairing); err != nil {
			return err
		}
		if err := tx.PutProposal(store.ProposalRecord{Proposal: proposal, SelfPrivateKey: crypto.EncodeKey(self.Private)}); err != nil {
			return err
		}
		_, err := tx.InsertHistory(e.outHistory(proposal.ID, pairingTopic, methodPropose, payload))
		return err
	})
	if err != nil {
		return nil, err
	}
	e.requestRearm()

	sent := sentRequest{id: proposal.ID, reply: e.addWaiter(ctx, proposal.ID, pairingTopic), span: trace.From(ctx)}
	if err := e.sendProposal(ctx, pairingTopic, symHex, payload); err != nil {
		e.removeWaiter(proposal.ID)
		_ = e.store.Update(context.Background(), func(tx *store.Tx) error { return tx.ForgetPairing(pairingTopic, proposal.ID) })
		e.unsubscribe(ctx, pairingTopic)
		return nil, err
	}
	trace.From(ctx).Step("proposal published", "id", proposal.ID)
	link := uri.Format(uri.Params{Topic: pairingTopic, Version: 2, SymKey: symHex,
		Relay: map[string]string{"protocol": relayProtocol}, ExpiryTimestamp: proposal.ExpiryTimestamp})
	return &Connection{URI: link, PairingTopic: pairingTopic, ProposalID: proposal.ID, Expiry: proposal.ExpiryTimestamp, e: e, proposal: sent}, nil
}

// sendProposal subscribes to the pairing topic, then publishes the proposal.
func (e *Engine) sendProposal(ctx context.Context, pairingTopic, symHex string, payload json.RawMessage) error {
	if err := e.relay.Subscribe(ctx, pairingTopic); err != nil {
		return fmt.Errorf("subscribe pairing topic: %w", err)
	}
	msg, err := encrypt(symHex, payload)
	if err != nil {
		return err
	}
	if _, err := e.relay.Request(ctx, "wc_proposeSession", map[string]any{
		"pairingTopic": pairingTopic, "sessionProposal": msg, "ttl": fiveMinutes,
	}); err != nil {
		return fmt.Errorf("propose session: %w", err)
	}
	return nil
}

type RequestParams struct {
	Topic   string
	ChainID string
	Method  string
	Params  json.RawMessage
	// Expiry is how long the wallet has to answer: 5 minutes to 7 days,
	// default 15 minutes.
	Expiry time.Duration
}

// Request sends a session request and waits for the wallet's answer or the
// request expiry.
func (e *Engine) Request(ctx context.Context, p RequestParams) (json.RawMessage, error) {
	s, err := e.loadSession(ctx, p.Topic)
	if err != nil {
		return nil, err
	}
	ttl, err := validateRequest(s, p)
	if err != nil {
		return nil, err
	}
	params := p.Params
	if params == nil {
		params = json.RawMessage("[]")
	}
	o := optsRequestReq
	o.ttl = ttl
	sent, err := e.sendRequest(ctx, s, methodRequest, requestParams{
		Request: sessionRequest{Method: p.Method, Params: params, ExpiryTimestamp: e.now() + ttl},
		ChainID: p.ChainID,
	}, o, nil)
	if err != nil {
		return nil, err
	}
	r, err := e.await(ctx, sent, time.Duration(ttl)*time.Second)
	if err != nil {
		return nil, err
	}
	return r.raw, nil
}
