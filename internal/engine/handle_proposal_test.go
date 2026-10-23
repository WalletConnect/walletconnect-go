package engine

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

func (h *harness) proposeParams() proposeParams {
	return proposeParams{
		Relays:   []types.Relay{{Protocol: relayProtocol}},
		Proposer: types.Participant{PublicKey: crypto.EncodeKey(keyPair(h.t).Public), Metadata: types.Metadata{Name: "dapp"}},
		OptionalNamespaces: map[string]types.ProposalNamespace{"eip155": {
			Chains: []string{"eip155:1"}, Methods: []string{"personal_sign"}, Events: []string{"chainChanged"},
		}},
		ExpiryTimestamp: h.clock.now().Unix() + fiveMinutes,
	}
}

func TestOnProposeEmitsAndStoresProposal(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedWalletPairing()
	id := jsonrpc.PayloadID()
	h.deliver(topic, symHex, request(id, methodPropose, h.proposeParams()))

	evs := eventsOf[types.EventSessionProposal](h)
	if len(evs) != 1 || evs[0].Proposal.ID != id || evs[0].Proposal.PairingTopic != topic {
		t.Fatalf("proposal events %+v", evs)
	}
	if h.proposal(id) == nil {
		t.Fatal("proposal not stored")
	}
	h.noPublishes(topic)
}

func TestOnProposeDefaultsMissingExpiry(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedWalletPairing()
	p := h.proposeParams()
	p.ExpiryTimestamp = 0
	id := jsonrpc.PayloadID()
	h.deliver(topic, symHex, request(id, methodPropose, p))
	if got, want := h.proposal(id).ExpiryTimestamp, h.clock.now().Unix()+fiveMinutes; got != want {
		t.Fatalf("expiry %d, want %d", got, want)
	}
}

func TestOnProposeAutoRejectsInvalidProposals(t *testing.T) {
	cases := map[string]struct {
		change func(*proposeParams)
		raw    any
		code   int
	}{
		"bad proposer key":       {change: func(p *proposeParams) { p.Proposer.PublicKey = "abc" }, code: 5},
		"relay without protocol": {change: func(p *proposeParams) { p.Relays = []types.Relay{{}} }, code: 5},
		"bad chain": {change: func(p *proposeParams) {
			p.OptionalNamespaces = map[string]types.ProposalNamespace{"eip155": {Chains: []string{"one"}, Methods: []string{}, Events: []string{}}}
		}, code: 5100},
		"bad required chain": {change: func(p *proposeParams) {
			p.RequiredNamespaces = map[string]types.ProposalNamespace{"eip155": {Chains: []string{"one"}, Methods: []string{}, Events: []string{}}}
		}, code: 5100},
		"scoped property outside namespaces": {change: func(p *proposeParams) {
			p.ScopedProperties = map[string]json.RawMessage{"solana": json.RawMessage(`{}`)}
		}, code: 5},
		"params not an object": {raw: "nope", code: 5},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			topic, symHex := h.seedWalletPairing()
			var params any = c.raw
			if c.change != nil {
				p := h.proposeParams()
				c.change(&p)
				params = p
			}
			id := jsonrpc.PayloadID()
			if !h.deliver(topic, symHex, request(id, methodPropose, params)) {
				t.Fatal("not acked")
			}
			h.mustReject(topic, symHex, c.code, optsProposeAutoReject.tag)
			if len(eventsOf[types.EventSessionProposal](h)) != 0 || h.proposal(id) != nil {
				t.Fatal("invalid proposal was accepted")
			}
		})
	}
}

func TestOnProposeIgnoresExpiredProposal(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedWalletPairing()
	p := h.proposeParams()
	p.ExpiryTimestamp = h.clock.now().Unix() - 1
	id := jsonrpc.PayloadID()
	h.deliver(topic, symHex, request(id, methodPropose, p))
	if len(eventsOf[types.EventSessionProposal](h)) != 0 || h.proposal(id) != nil {
		t.Fatal("expired proposal was accepted")
	}
	if h.history(id) == nil {
		t.Fatal("expired proposal not recorded")
	}
	h.noPublishes(topic)
}

func TestOnProposeIgnoresProposalOnDAppPairing(t *testing.T) {
	h := newHarness(t)
	topic, symHex, _, _ := h.seedDAppProposal()
	h.deliver(topic, symHex, request(jsonrpc.PayloadID(), methodPropose, h.proposeParams()))
	if len(eventsOf[types.EventSessionProposal](h)) != 0 {
		t.Fatal("dApp accepted a proposal")
	}
	h.noPublishes(topic)
}

func TestOnProposeResponseStoresPendingSession(t *testing.T) {
	h := newHarness(t)
	pairingTopic, symHex, id, self := h.seedDAppProposal()
	wallet := keyPair(t)
	h.deliver(pairingTopic, symHex, result(id, proposeResult{
		Relay: types.Relay{Protocol: relayProtocol}, ResponderPublicKey: crypto.EncodeKey(wallet.Public),
	}))

	sym, err := crypto.DeriveSymKey(wallet.Private, self.Public)
	if err != nil {
		t.Fatal(err)
	}
	sessionTopic := crypto.Topic(sym)
	var pending *store.PendingSession
	h.view(func(tx *store.Tx) (err error) {
		pending, err = tx.PendingSession(sessionTopic)
		return err
	})
	if pending == nil || pending.SymKey != crypto.EncodeKey(sym) || pending.ProposalID != id || pending.PairingTopic != pairingTopic {
		t.Fatalf("pending session %+v", pending)
	}
	if len(h.relay.asyncSubs) != 1 || h.relay.asyncSubs[0] != sessionTopic {
		t.Fatalf("subscribed %v, want the session topic", h.relay.asyncSubs)
	}
	if h.history(id).Response == nil {
		t.Fatal("proposal response not recorded")
	}
}

func TestOnProposeResponseErrorFailsConnection(t *testing.T) {
	h := newHarness(t)
	pairingTopic, symHex, id, _ := h.seedDAppProposal()
	ch := h.e.addWaiter(h.ctx(), id, pairingTopic, "wc_sessionRequest", "")
	h.deliver(pairingTopic, symHex, rpcError(id, 5000))

	if r := received(t, ch); r.rpcErr == nil || r.rpcErr.Code != 5000 {
		t.Fatalf("waiter got %+v, want the rejection", r)
	}
	if h.pairing(pairingTopic) != nil || h.proposal(id) != nil {
		t.Fatal("rejected proposal left state behind")
	}
	if h.relay.unsubscribed(pairingTopic) != 1 {
		t.Fatal("pairing topic not unsubscribed")
	}
}

func TestOnProposeResponseWithBadKeyIsDropped(t *testing.T) {
	h := newHarness(t)
	pairingTopic, symHex, id, _ := h.seedDAppProposal()
	if !h.deliver(pairingTopic, symHex, result(id, proposeResult{ResponderPublicKey: "zz"})) {
		t.Fatal("not acked")
	}
	if !h.deliver(pairingTopic, symHex, result(id, "not an object")) {
		t.Fatal("not acked")
	}
	if len(h.relay.asyncSubs) != 0 {
		t.Fatal("subscribed to a session topic")
	}
}

// seedPending stores a dApp proposal and the pending session the wallet's
// approval created for it.
func (h *harness) seedPending() (sessionTopic, symHex, pairingTopic string, proposalID int64) {
	pairingTopic, _, proposalID, self := h.seedDAppProposal()
	sym := randomKey(h.t)
	sessionTopic, symHex = crypto.Topic(sym), crypto.EncodeKey(sym)
	h.update(func(tx *store.Tx) error {
		return tx.PutPendingSession(store.PendingSession{SessionTopic: sessionTopic, SymKey: symHex, ProposalID: proposalID,
			PairingTopic: pairingTopic, SelfPublicKey: crypto.EncodeKey(self.Public), CreatedAt: h.clock.now().Unix()})
	})
	return sessionTopic, symHex, pairingTopic, proposalID
}

func (h *harness) settleParams() settleParams {
	return settleParams{
		Relay: types.Relay{Protocol: relayProtocol}, Namespaces: evmSession("eip155:1"),
		Controller: types.Participant{PublicKey: crypto.EncodeKey(keyPair(h.t).Public), Metadata: types.Metadata{Name: "wallet"}},
		Expiry:     h.clock.now().Unix() + sevenDays,
	}
}

func TestOnSettleCompletesConnection(t *testing.T) {
	h := newHarness(t)
	sessionTopic, symHex, pairingTopic, proposalID := h.seedPending()
	ch := h.e.addWaiter(h.ctx(), proposalID, pairingTopic, "wc_sessionRequest", "")
	id := jsonrpc.PayloadID()
	h.deliver(sessionTopic, symHex, request(id, methodSettle, h.settleParams()))

	h.mustResult(sessionTopic, symHex, id, optsSettleRes.tag)
	if r := received(t, ch); r.session == nil || r.session.Topic != sessionTopic {
		t.Fatalf("waiter got %+v, want the session", r)
	}
	if evs := eventsOf[types.EventSessionConnect](h); len(evs) != 1 {
		t.Fatalf("%d connect events, want 1", len(evs))
	}
	if h.session(sessionTopic) == nil || h.pairing(pairingTopic) != nil || h.proposal(proposalID) != nil {
		t.Fatal("settle did not replace the pairing with a session")
	}
	if h.relay.unsubscribed(pairingTopic) != 1 {
		t.Fatal("pairing topic not unsubscribed")
	}
}

func TestOnSettleRejectsInvalidSettlement(t *testing.T) {
	cases := map[string]struct {
		change func(*settleParams)
		raw    any
		code   int
	}{
		"expired":              {change: func(p *settleParams) { p.Expiry = 1 }, code: 6},
		"no relay protocol":    {change: func(p *settleParams) { p.Relay = types.Relay{} }, code: 5},
		"no controller":        {change: func(p *settleParams) { p.Controller.PublicKey = "" }, code: 5},
		"no namespaces":        {change: func(p *settleParams) { p.Namespaces = nil }, code: 5},
		"bad account":          {change: func(p *settleParams) { p.Namespaces["eip155"] = types.SessionNamespace{Accounts: []string{"x"}} }, code: 5103},
		"params not an object": {raw: 7, code: 5},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			sessionTopic, symHex, pairingTopic, _ := h.seedPending()
			var params any = c.raw
			if c.change != nil {
				p := h.settleParams()
				c.change(&p)
				params = p
			}
			h.deliver(sessionTopic, symHex, request(jsonrpc.PayloadID(), methodSettle, params))
			h.mustReject(sessionTopic, symHex, c.code, optsSettleRes.tag)
			if h.session(sessionTopic) != nil || h.pairing(pairingTopic) == nil {
				t.Fatal("invalid settlement created a session")
			}
		})
	}
}

func TestOnSettleWithoutPendingSessionIsIgnored(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(false)
	id := jsonrpc.PayloadID()
	h.deliver(topic, symHex, request(id, methodSettle, h.settleParams()))
	h.noPublishes(topic)
	if len(eventsOf[types.EventSessionConnect](h)) != 0 || h.history(id) == nil {
		t.Fatal("stray settle was not just recorded")
	}
}

// seedUnacknowledged stores a wallet session whose settle request awaits the
// dApp's answer.
func (h *harness) seedUnacknowledged() (topic, symHex string, settleID int64, ch chan waitResult) {
	topic, symHex = h.seedSession(true)
	settleID = jsonrpc.PayloadID()
	h.update(func(tx *store.Tx) error {
		s, err := tx.Session(topic)
		if err != nil {
			return err
		}
		s.Acknowledged = false
		if err := tx.PutSession(*s); err != nil {
			return err
		}
		_, err = tx.InsertHistory(store.History{ID: settleID, Topic: topic, Method: methodSettle, Direction: store.DirectionOut})
		return err
	})
	return topic, symHex, settleID, h.e.addWaiter(h.ctx(), settleID, topic, "wc_sessionRequest", "")
}

func TestOnSettleResponseAcknowledgesSession(t *testing.T) {
	h := newHarness(t)
	topic, symHex, settleID, ch := h.seedUnacknowledged()
	h.deliver(topic, symHex, result(settleID, true))
	if r := received(t, ch); r.err != nil || r.rpcErr != nil {
		t.Fatalf("waiter got %+v", r)
	}
	if !h.session(topic).Acknowledged {
		t.Fatal("session not acknowledged")
	}
}

func TestOnSettleResponseErrorDeletesSession(t *testing.T) {
	h := newHarness(t)
	topic, symHex, settleID, ch := h.seedUnacknowledged()
	h.deliver(topic, symHex, rpcError(settleID, 7000))
	var rpcErr *jsonrpc.Error
	if r := received(t, ch); r.rpcErr == nil || !errors.As(r.rpcErr, &rpcErr) || rpcErr.Code != 7000 {
		t.Fatalf("waiter got %+v", r)
	}
	if h.session(topic) != nil || h.relay.unsubscribed(topic) != 1 {
		t.Fatal("rejected settlement left the session")
	}
}
