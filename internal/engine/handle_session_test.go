package engine

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

func signRequest(chainID, method string) requestParams {
	return requestParams{ChainID: chainID, Request: sessionRequest{Method: method, Params: json.RawMessage(`["0x1"]`)}}
}

func TestOnRequestStoresAndEmitsRequest(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(true)
	id := jsonrpc.PayloadID()
	h.deliver(topic, symHex, request(id, methodRequest, signRequest("eip155:1", "personal_sign")))

	evs := eventsOf[types.EventSessionRequest](h)
	if len(evs) != 1 || evs[0].ID != id || evs[0].ExpiryTimestamp != h.clock.now().Unix()+requestTTLDefault {
		t.Fatalf("request events %+v", evs)
	}
	reqs, err := h.e.PendingRequests(h.ctx())
	if err != nil || len(reqs) != 1 || reqs[0].ID != id {
		t.Fatalf("pending requests %+v, %v", reqs, err)
	}
	h.noPublishes(topic)
}

func TestOnRequestRejectsInvalidRequests(t *testing.T) {
	cases := map[string]any{
		"chain not in session":  signRequest("eip155:137", "personal_sign"),
		"method not in session": signRequest("eip155:1", "eth_sendTransaction"),
		"no method":             signRequest("eip155:1", ""),
		"params not an object":  "nope",
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			topic, symHex := h.seedSession(true)
			h.deliver(topic, symHex, request(jsonrpc.PayloadID(), methodRequest, params))
			h.mustReject(topic, symHex, 5, optsRequestRes.tag)
			if len(eventsOf[types.EventSessionRequest](h)) != 0 {
				t.Fatal("invalid request emitted")
			}
		})
	}
}

func TestOnRequestIgnoresExpiredRequest(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(true)
	p := signRequest("eip155:1", "personal_sign")
	p.Request.ExpiryTimestamp = h.clock.now().Unix() - 1
	h.deliver(topic, symHex, request(jsonrpc.PayloadID(), methodRequest, p))
	if len(eventsOf[types.EventSessionRequest](h)) != 0 {
		t.Fatal("expired request emitted")
	}
	h.noPublishes(topic)
}

// Session messages on a topic with a key but no session, here a pairing,
// are answered with NO_MATCHING_KEY, except delete, which gets no answer.
func TestSessionMessagesWithoutSession(t *testing.T) {
	cases := map[string]struct {
		params any
		tag    int
	}{
		methodRequest: {signRequest("eip155:1", "personal_sign"), optsRequestRes.tag},
		methodEvent:   {eventParams{ChainID: "eip155:1", Event: sessionEvent{Name: "chainChanged"}}, optsEventRes.tag},
		methodUpdate:  {updateParams{Namespaces: evmSession("eip155:1")}, optsUpdateRes.tag},
		methodExtend:  {empty{}, optsExtendRes.tag},
		methodPing:    {empty{}, optsPingRes.tag},
	}
	for method, c := range cases {
		t.Run(method, func(t *testing.T) {
			h := newHarness(t)
			topic, symHex := h.seedWalletPairing()
			h.deliver(topic, symHex, request(jsonrpc.PayloadID(), method, c.params))
			h.mustReject(topic, symHex, 2, c.tag)
		})
	}
	t.Run(methodDelete, func(t *testing.T) {
		h := newHarness(t)
		topic, symHex := h.seedWalletPairing()
		id := jsonrpc.PayloadID()
		h.deliver(topic, symHex, request(id, methodDelete, jsonrpc.Error{Code: 6000}))
		h.noPublishes(topic)
		if h.history(id) == nil || h.pairing(topic) == nil {
			t.Fatal("stray delete was not just recorded")
		}
	})
}

func chainChanged(chainID string) eventParams {
	return eventParams{ChainID: chainID, Event: sessionEvent{Name: "chainChanged", Data: json.RawMessage(`"0x1"`)}}
}

func TestOnEventEmitsInOrder(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(false)
	newer := jsonrpc.PayloadID()
	older := newer - 5000 // 5 ms earlier
	h.deliver(topic, symHex, request(newer, methodEvent, chainChanged("eip155:1")))
	h.deliver(topic, symHex, request(older, methodEvent, chainChanged("eip155:1")))

	evs := eventsOf[types.EventSessionEvent](h)
	if len(evs) != 1 || evs[0].ID != newer || evs[0].Name != "chainChanged" {
		t.Fatalf("events %+v, want only the newer one", evs)
	}
	if h.history(older) == nil {
		t.Fatal("out-of-order event not recorded")
	}
	h.noPublishes(topic)
	if s := h.e.Stats(); s.EventOrder != 1 {
		t.Fatalf("event order entries %d, want 1", s.EventOrder)
	}
}

func TestOnEventRejectsInvalidEvents(t *testing.T) {
	cases := map[string]any{
		"chain not in session": chainChanged("eip155:137"),
		"event not in session": eventParams{ChainID: "eip155:1", Event: sessionEvent{Name: "accountsChanged"}},
		"params not an object": 1,
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			topic, symHex := h.seedSession(false)
			h.deliver(topic, symHex, request(jsonrpc.PayloadID(), methodEvent, params))
			h.mustReject(topic, symHex, 5, optsEventRes.tag)
		})
	}
}

func TestOnUpdateAppliesNamespaces(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(false)
	ns := evmSession("eip155:1", "eip155:137")
	id := jsonrpc.PayloadID()
	h.deliver(topic, symHex, request(id, methodUpdate, updateParams{Namespaces: ns}))

	h.mustResult(topic, symHex, id, optsUpdateRes.tag)
	if got := h.session(topic).Namespaces["eip155"].Chains; len(got) != 2 {
		t.Fatalf("chains %v, want 2", got)
	}
	if evs := eventsOf[types.EventSessionUpdate](h); len(evs) != 1 || evs[0].ID != id {
		t.Fatalf("update events %+v", evs)
	}
}

func TestOnUpdateRejections(t *testing.T) {
	t.Run("not from the controller", func(t *testing.T) {
		h := newHarness(t)
		topic, symHex := h.seedSession(true)
		h.deliver(topic, symHex, request(jsonrpc.PayloadID(), methodUpdate, updateParams{Namespaces: evmSession("eip155:1")}))
		h.mustReject(topic, symHex, 3003, optsUpdateRes.tag)
	})
	t.Run("older than the last update", func(t *testing.T) {
		h := newHarness(t)
		topic, symHex := h.seedSession(false)
		newer := jsonrpc.PayloadID()
		h.deliver(topic, symHex, request(newer, methodUpdate, updateParams{Namespaces: evmSession("eip155:1")}))
		h.relay.reset()
		h.deliver(topic, symHex, request(newer-5000, methodUpdate, updateParams{Namespaces: evmSession("eip155:1")}))
		h.mustReject(topic, symHex, 1003, optsUpdateRes.tag)
	})
	t.Run("invalid namespaces", func(t *testing.T) {
		h := newHarness(t)
		topic, symHex := h.seedSession(false)
		bad := map[string]types.SessionNamespace{"eip155": {Accounts: []string{"nope"}, Methods: []string{}, Events: []string{}}}
		h.deliver(topic, symHex, request(jsonrpc.PayloadID(), methodUpdate, updateParams{Namespaces: bad}))
		h.mustReject(topic, symHex, 5103, optsUpdateRes.tag)
	})
	t.Run("params not an object", func(t *testing.T) {
		h := newHarness(t)
		topic, symHex := h.seedSession(false)
		h.deliver(topic, symHex, request(jsonrpc.PayloadID(), methodUpdate, "x"))
		h.mustReject(topic, symHex, 5, optsUpdateRes.tag)
	})
}

func TestOnExtendMovesExpiry(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(false)
	h.clock.advance(oneDay * time.Second)
	id := jsonrpc.PayloadID()
	h.deliver(topic, symHex, request(id, methodExtend, empty{}))

	h.mustResult(topic, symHex, id, optsExtendRes.tag)
	want := h.clock.now().Unix() + sevenDays
	if got := h.session(topic).Expiry; got != want {
		t.Fatalf("expiry %d, want %d", got, want)
	}
	if evs := eventsOf[types.EventSessionExtend](h); len(evs) != 1 || evs[0].Expiry != want {
		t.Fatalf("extend events %+v", evs)
	}
}

func TestOnDeleteRemovesSession(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(false)
	h.deliver(topic, symHex, request(jsonrpc.PayloadID(), methodEvent, chainChanged("eip155:1")))
	ch := h.e.addWaiter(h.ctx(), jsonrpc.PayloadID(), topic, "wc_sessionRequest", "")

	h.deliver(topic, symHex, request(jsonrpc.PayloadID(), methodDelete, jsonrpc.Error{Code: 6000, Message: "User disconnected."}))

	if h.session(topic) != nil || h.relay.unsubscribed(topic) != 1 {
		t.Fatal("session not removed")
	}
	if r := received(t, ch); !errors.Is(r.err, types.ErrSessionDeleted) {
		t.Fatalf("waiter got %+v, want ErrSessionDeleted", r)
	}
	evs := eventsOf[types.EventSessionDelete](h)
	if len(evs) != 1 || evs[0].Reason.Code != 6000 {
		t.Fatalf("delete events %+v", evs)
	}
	h.noPublishes(topic)
	if s := h.e.Stats(); s != (Stats{}) {
		t.Fatalf("stats %+v after delete, want zero", s)
	}
}
