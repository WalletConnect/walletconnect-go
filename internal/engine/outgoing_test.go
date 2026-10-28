package engine

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/sdkerr"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/types"
	"github.com/WalletConnect/walletconnect-go/internal/uri"
)

var errRelay = errors.New("relay said no")

func evmConnect() ConnectParams {
	return ConnectParams{OptionalNamespaces: map[string]types.ProposalNamespace{"eip155": {
		Chains: []string{"eip155:1"}, Methods: []string{"personal_sign"}, Events: []string{"chainChanged"},
	}}}
}

func TestConnectStoresAndSendsProposal(t *testing.T) {
	h := newHarness(t)
	conn, err := h.e.Connect(h.ctx(), evmConnect())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(conn.URI, "wc:"+conn.PairingTopic+"@2?") {
		t.Fatalf("uri %s", conn.URI)
	}
	if h.pairing(conn.PairingTopic) == nil || h.proposal(conn.ProposalID) == nil {
		t.Fatal("proposal not stored")
	}
	if len(h.relay.subs) != 1 || h.relay.subs[0] != conn.PairingTopic {
		t.Fatalf("subscribed %v", h.relay.subs)
	}
	if n := len(h.relay.requests); n != 1 || h.relay.requests[0].method != "wc_proposeSession" {
		t.Fatalf("relay calls %+v", h.relay.requests)
	}
}

func TestConnectCleansUpWhenRelayFails(t *testing.T) {
	for _, method := range []string{"irn_subscribe", "wc_proposeSession"} {
		t.Run(method, func(t *testing.T) {
			h := newHarness(t)
			h.relay.failOn(method, errRelay)
			if _, err := h.e.Connect(h.ctx(), evmConnect()); !errors.Is(err, errRelay) {
				t.Fatalf("got %v, want the relay error", err)
			}
			var pairings []store.Pairing
			var proposals []store.ProposalRecord
			h.view(func(tx *store.Tx) (err error) {
				if pairings, err = tx.Pairings(); err != nil {
					return err
				}
				proposals, err = tx.Proposals()
				return err
			})
			if len(pairings) != 0 || len(proposals) != 0 || h.e.Stats().Waiters != 0 {
				t.Fatal("failed connect left state behind")
			}
		})
	}
}

func TestConnectRejectsInvalidParams(t *testing.T) {
	h := newHarness(t)
	bad := ConnectParams{OptionalNamespaces: map[string]types.ProposalNamespace{"eip155": {Chains: []string{"one"}}}}
	scoped := evmConnect()
	scoped.ScopedProperties = map[string]json.RawMessage{"solana": json.RawMessage(`{}`)}
	for _, p := range []ConnectParams{bad, scoped} {
		if _, err := h.e.Connect(h.ctx(), p); !errors.Is(err, types.ErrInvalidParams) {
			t.Fatalf("got %v, want ErrInvalidParams", err)
		}
	}
	if len(h.relay.requests) != 0 {
		t.Fatal("invalid connect reached the relay")
	}
}

func pairingURI(topic, symHex string, expiry int64) string {
	return uri.Format(uri.Params{Topic: topic, Version: 2, SymKey: symHex,
		Relay: map[string]string{"protocol": relayProtocol}, ExpiryTimestamp: expiry})
}

func TestPair(t *testing.T) {
	h := newHarness(t)
	sym := randomKey(t)
	topic, symHex := crypto.Topic(sym), crypto.EncodeKey(sym)
	link := pairingURI(topic, symHex, h.clock.now().Unix()+fiveMinutes)
	if err := h.e.Pair(h.ctx(), link); err != nil {
		t.Fatal(err)
	}
	if len(h.relay.subs) != 1 || h.pairing(topic) == nil {
		t.Fatal("pairing not stored and subscribed")
	}

	// Pairing again before answering re-emits the proposal.
	h.deliver(topic, symHex, request(jsonrpc.PayloadID(), methodPropose, h.proposeParams()))
	if err := h.e.Pair(h.ctx(), link); err != nil {
		t.Fatal(err)
	}
	if n := len(eventsOf[types.EventSessionProposal](h)); n != 2 {
		t.Fatalf("%d proposal events, want 2", n)
	}
	if len(h.relay.subs) != 1 {
		t.Fatal("subscribed twice")
	}
}

func TestPairRejectsBadURIs(t *testing.T) {
	h := newHarness(t)
	sym := randomKey(t)
	topic, symHex := crypto.Topic(sym), crypto.EncodeKey(sym)
	now := h.clock.now().Unix()
	dappTopic, dappSym, _, _ := h.seedDAppProposal()
	cases := map[string]struct {
		link string
		want error
	}{
		"not a uri":     {"https://example.com", types.ErrInvalidParams},
		"version 1":     {strings.Replace(pairingURI(topic, symHex, now+60), "@2?", "@1?", 1), types.ErrInvalidParams},
		"bad key":       {pairingURI(topic, "abcd", now+60), types.ErrInvalidParams},
		"expired":       {pairingURI(topic, symHex, now-1), types.ErrExpired},
		"own dApp link": {pairingURI(dappTopic, dappSym, now+60), types.ErrPairingExists},
	}
	for name, c := range cases {
		if err := h.e.Pair(h.ctx(), c.link); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
	if h.pairing(topic) != nil {
		t.Fatal("bad uri stored a pairing")
	}
}

func TestPairCleansUpWhenSubscribeFails(t *testing.T) {
	h := newHarness(t)
	h.relay.failOn("irn_subscribe", errRelay)
	sym := randomKey(t)
	topic := crypto.Topic(sym)
	if err := h.e.Pair(h.ctx(), pairingURI(topic, crypto.EncodeKey(sym), 0)); !errors.Is(err, errRelay) {
		t.Fatalf("got %v, want the relay error", err)
	}
	if h.pairing(topic) != nil {
		t.Fatal("pairing kept after failed subscribe")
	}
}

// receiveProposal pairs the wallet and delivers a valid proposal with the
// given required namespaces.
func (h *harness) receiveProposal(required map[string]types.ProposalNamespace) (pairingTopic, symHex string, id int64) {
	pairingTopic, symHex = h.seedWalletPairing()
	p := h.proposeParams()
	p.RequiredNamespaces = required
	id = jsonrpc.PayloadID()
	h.deliver(pairingTopic, symHex, request(id, methodPropose, p))
	return pairingTopic, symHex, id
}

func TestApproveRollsBackWhenRelayFails(t *testing.T) {
	h := newHarness(t)
	pairingTopic, _, id := h.receiveProposal(nil)
	h.relay.failOn("wc_approveSession", errRelay)
	params := ApproveParams{ProposalID: id, Namespaces: evmSession("eip155:1")}
	if _, err := h.e.Approve(h.ctx(), params); !errors.Is(err, errRelay) {
		t.Fatalf("got %v, want the relay error", err)
	}
	sessions, _ := h.e.Sessions(h.ctx())
	if len(sessions) != 0 || h.e.Stats().Waiters != 0 || h.history(id).Response != nil {
		t.Fatal("failed approval left state behind")
	}

	// The proposal can still be approved.
	h.relay.failOn("wc_approveSession", nil)
	approval, err := h.e.Approve(h.ctx(), params)
	if err != nil {
		t.Fatal(err)
	}
	if h.session(approval.Session.Topic) == nil || h.proposal(id) != nil || !h.pairing(pairingTopic).Retired {
		t.Fatal("approval did not settle and retire the pairing")
	}
	if h.relay.unsubscribed(pairingTopic) != 1 {
		t.Fatal("pairing topic not unsubscribed")
	}
	if !slices.Equal(h.relay.serverSubs, []string{approval.Session.Topic}) {
		t.Fatalf("relay subscribed %v, want the session topic", h.relay.serverSubs)
	}
}

func TestApproveRejectsBadApprovals(t *testing.T) {
	h := newHarness(t)
	required := map[string]types.ProposalNamespace{"eip155": {Chains: []string{"eip155:1"}, Methods: []string{"personal_sign"}, Events: []string{}}}
	_, _, id := h.receiveProposal(required)

	cases := map[string]struct {
		p    ApproveParams
		want error
	}{
		"unknown proposal":   {ApproveParams{ProposalID: 1, Namespaces: evmSession("eip155:1")}, types.ErrNotFound},
		"missing chain":      {ApproveParams{ProposalID: id, Namespaces: evmSession("eip155:137")}, types.ErrInvalidParams},
		"invalid namespaces": {ApproveParams{ProposalID: id}, types.ErrInvalidParams},
		"scoped outside":     {ApproveParams{ProposalID: id, Namespaces: evmSession("eip155:1"), ScopedProperties: map[string]json.RawMessage{"solana": nil}}, types.ErrInvalidParams},
	}
	for name, c := range cases {
		if _, err := h.e.Approve(h.ctx(), c.p); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
	h.clock.advance((fiveMinutes + 1) * time.Second)
	if _, err := h.e.Approve(h.ctx(), ApproveParams{ProposalID: id, Namespaces: evmSession("eip155:1")}); !errors.Is(err, types.ErrExpired) {
		t.Fatalf("got %v, want ErrExpired", err)
	}
	if len(h.relay.requests) != 0 {
		t.Fatal("a bad approval reached the relay")
	}
}

func TestRejectAnswersAndRetiresPairing(t *testing.T) {
	h := newHarness(t)
	pairingTopic, symHex, id := h.receiveProposal(nil)
	if err := h.e.Reject(h.ctx(), id, nil); err != nil {
		t.Fatal(err)
	}
	h.mustReject(pairingTopic, symHex, 5000, optsProposeReject.tag)
	if h.proposal(id) != nil || !h.pairing(pairingTopic).Retired || h.relay.unsubscribed(pairingTopic) != 1 {
		t.Fatal("rejected proposal not cleaned up")
	}
	link := pairingURI(pairingTopic, symHex, h.clock.now().Unix()+60)
	if err := h.e.Pair(h.ctx(), link); !errors.Is(err, types.ErrPairingExists) {
		t.Fatalf("re-pair got %v, want ErrPairingExists", err)
	}
}

func TestRejectKeepsProposalWhenPublishFails(t *testing.T) {
	h := newHarness(t)
	_, _, id := h.receiveProposal(nil)
	h.relay.failOn("irn_publish", errRelay)
	if err := h.e.Reject(h.ctx(), id, sdkerr.New(sdkerr.UserRejectedChains, "")); !errors.Is(err, errRelay) {
		t.Fatalf("got %v, want the relay error", err)
	}
	if h.proposal(id) == nil {
		t.Fatal("proposal dropped although the peer never heard")
	}
}

func TestUpdate(t *testing.T) {
	t.Run("rolls back when publish fails", func(t *testing.T) {
		h := newHarness(t)
		topic, _ := h.seedSession(true)
		h.relay.failOn("irn_publish", errRelay)
		if _, err := h.e.Update(h.ctx(), topic, evmSession("eip155:1", "eip155:137")); !errors.Is(err, errRelay) {
			t.Fatalf("got %v, want the relay error", err)
		}
		if n := len(h.session(topic).Namespaces["eip155"].Chains); n != 1 || h.e.Stats().Waiters != 0 {
			t.Fatal("failed update was applied")
		}
	})
	t.Run("only the controller", func(t *testing.T) {
		h := newHarness(t)
		topic, _ := h.seedSession(false)
		if _, err := h.e.Update(h.ctx(), topic, evmSession("eip155:1")); !errors.Is(err, types.ErrUnauthorized) {
			t.Fatalf("got %v, want ErrUnauthorized", err)
		}
	})
	t.Run("peer answers", func(t *testing.T) {
		h := newHarness(t)
		topic, symHex := h.seedSession(true)
		ack, err := h.e.Update(h.ctx(), topic, evmSession("eip155:1", "eip155:137"))
		if err != nil {
			t.Fatal(err)
		}
		sent, tag := h.reply(topic, symHex)
		if sent.Method != methodUpdate || tag != optsUpdateReq.tag {
			t.Fatalf("published %s tag %d", sent.Method, tag)
		}
		h.deliver(topic, symHex, rpcError(sent.ID, 3003))
		var rpcErr *jsonrpc.Error
		if err := ack.Wait(h.ctx()); !errors.As(err, &rpcErr) || rpcErr.Code != 3003 {
			t.Fatalf("got %v, want the peer's error", err)
		}
	})
}

func TestRespondChecksTheRequest(t *testing.T) {
	h := newHarness(t)
	topic, _ := h.seedSession(true)
	other, _ := h.seedSession(true)
	req := types.PendingRequest{ID: jsonrpc.PayloadID(), Topic: topic, ExpiryTimestamp: h.clock.now().Unix() + 60}
	h.update(func(tx *store.Tx) error { return tx.PutRequest(req) })
	ok := json.RawMessage(`"0x"`)

	if err := h.e.Respond(h.ctx(), topic, req.ID, nil, nil); !errors.Is(err, types.ErrInvalidParams) {
		t.Fatalf("no answer: got %v", err)
	}
	if err := h.e.Respond(h.ctx(), topic, 1, ok, nil); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("unknown id: got %v", err)
	}
	if err := h.e.Respond(h.ctx(), other, req.ID, ok, nil); !errors.Is(err, types.ErrInvalidParams) {
		t.Fatalf("wrong topic: got %v", err)
	}
	h.update(func(tx *store.Tx) error { return tx.DeleteSession(topic) })
	if err := h.e.Respond(h.ctx(), topic, req.ID, ok, nil); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("deleted session: got %v", err)
	}
	if reqs, _ := h.e.PendingRequests(h.ctx()); len(reqs) != 0 {
		t.Fatal("request of a deleted session kept")
	}
}

func TestRequestValidatesAndReturnsAnswer(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(false)
	bad := []RequestParams{
		{Topic: topic, ChainID: "eip155:137", Method: "personal_sign"},
		{Topic: topic, ChainID: "eip155:1", Method: "eth_sign"},
		{Topic: topic, ChainID: "eip155:1", Method: "personal_sign", Expiry: time.Minute},
		{Topic: topic, ChainID: "eip155:1", Method: "personal_sign", Expiry: 8 * 24 * time.Hour},
	}
	for _, p := range bad {
		if _, err := h.e.Request(h.ctx(), p); !errors.Is(err, types.ErrInvalidParams) {
			t.Fatalf("%+v: got %v, want ErrInvalidParams", p, err)
		}
	}

	done := make(chan json.RawMessage)
	go func() {
		res, err := h.e.Request(h.ctx(), RequestParams{Topic: topic, ChainID: "eip155:1", Method: "personal_sign", Expiry: time.Hour})
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	sent := h.awaitPublish(topic, symHex)
	var p requestParams
	if err := json.Unmarshal(sent.Params, &p); err != nil || p.Request.ExpiryTimestamp != h.clock.now().Unix()+3600 || string(p.Request.Params) != "[]" {
		t.Fatalf("published params %s, %v", sent.Params, err)
	}
	h.deliver(topic, symHex, result(sent.ID, "0xsig"))
	if res := <-done; string(res) != `"0xsig"` {
		t.Fatalf("result %s", res)
	}
}

func TestEmitValidatesEvent(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(true)
	if err := h.e.Emit(h.ctx(), topic, "eip155:1", "accountsChanged", nil); !errors.Is(err, types.ErrInvalidParams) {
		t.Fatalf("got %v, want ErrInvalidParams", err)
	}
	if err := h.e.Emit(h.ctx(), topic, "eip155:137", "chainChanged", nil); !errors.Is(err, types.ErrInvalidParams) {
		t.Fatalf("got %v, want ErrInvalidParams", err)
	}
	if err := h.e.Emit(h.ctx(), topic, "eip155:1", "chainChanged", nil); err != nil {
		t.Fatal(err)
	}
	sent, tag := h.reply(topic, symHex)
	if sent.Method != methodEvent || tag != optsEventReq.tag {
		t.Fatalf("published %s tag %d", sent.Method, tag)
	}
}

func TestDisconnect(t *testing.T) {
	t.Run("pairing", func(t *testing.T) {
		h := newHarness(t)
		pairingTopic, _, id, _ := h.seedDAppProposal()
		if err := h.e.Disconnect(h.ctx(), pairingTopic); err != nil {
			t.Fatal(err)
		}
		if h.pairing(pairingTopic) != nil || h.proposal(id) != nil || h.relay.unsubscribed(pairingTopic) != 1 {
			t.Fatal("pairing not forgotten")
		}
		h.noPublishes(pairingTopic)
	})
	t.Run("unknown topic", func(t *testing.T) {
		h := newHarness(t)
		if err := h.e.Disconnect(h.ctx(), "nope"); !errors.Is(err, types.ErrNotFound) {
			t.Fatalf("got %v, want ErrNotFound", err)
		}
	})
	t.Run("session kept when publish fails", func(t *testing.T) {
		h := newHarness(t)
		topic, _ := h.seedSession(false)
		h.relay.failOn("irn_publish", errRelay)
		if err := h.e.Disconnect(h.ctx(), topic); !errors.Is(err, errRelay) {
			t.Fatalf("got %v, want the relay error", err)
		}
		if h.session(topic) == nil {
			t.Fatal("session deleted although the peer never heard")
		}
	})
	t.Run("session", func(t *testing.T) {
		h := newHarness(t)
		topic, symHex := h.seedSession(false)
		if err := h.e.Disconnect(h.ctx(), topic); err != nil {
			t.Fatal(err)
		}
		sent, tag := h.reply(topic, symHex)
		var reason jsonrpc.Error
		if err := json.Unmarshal(sent.Params, &reason); err != nil || sent.Method != methodDelete || reason.Code != 6000 || tag != optsDeleteReq.tag {
			t.Fatalf("published %s %s tag %d", sent.Method, sent.Params, tag)
		}
		if h.session(topic) != nil || h.relay.unsubscribed(topic) != 1 {
			t.Fatal("session not removed")
		}
	})
}

// awaitPublish waits for a blocked call on another goroutine to publish on
// topic and returns the message.
func (h *harness) awaitPublish(topic, symHex string) *jsonrpc.Message {
	h.t.Helper()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if len(h.relay.publishes(topic)) == 1 {
			msg, _ := h.reply(topic, symHex)
			return msg
		}
	}
	h.t.Fatal("nothing published")
	return nil
}

func TestPingWaitsForPeer(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(false)
	done := make(chan error)
	go func() { done <- h.e.Ping(h.ctx(), topic) }()
	sent := h.awaitPublish(topic, symHex)
	h.deliver(topic, symHex, result(sent.ID, true))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := h.e.Ping(h.ctx(), "nope"); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
