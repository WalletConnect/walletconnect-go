package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

// call returns the params of the last relay call with the method.
func (h *harness) call(method string) map[string]any {
	h.t.Helper()
	h.relay.mu.Lock()
	defer h.relay.mu.Unlock()
	for i := len(h.relay.requests) - 1; i >= 0; i-- {
		if h.relay.requests[i].method == method {
			return h.relay.requests[i].params
		}
	}
	h.t.Fatalf("no %s call", method)
	return nil
}

// lastPublish returns the params of the last irn_publish on the topic.
func (h *harness) lastPublish(topic string) map[string]any {
	h.t.Helper()
	pubs := h.relay.publishes(topic)
	if len(pubs) == 0 {
		h.t.Fatalf("nothing published on %s", short(topic))
	}
	return pubs[len(pubs)-1]
}

// seedSessionAllowing seeds a session whose eip155 namespace allows methods.
func seedSessionAllowing(h *harness, asWallet bool, methods ...string) (topic, symHex string) {
	topic, symHex = h.seedSession(asWallet)
	h.update(func(tx *store.Tx) error {
		s, err := tx.Session(topic)
		if err != nil {
			return err
		}
		ns := s.Namespaces["eip155"]
		ns.Methods = methods
		s.Namespaces["eip155"] = ns
		return tx.PutSession(*s)
	})
	return topic, symHex
}

func TestTNVOnProposal(t *testing.T) {
	h := newHarness(t)
	conn, err := h.e.Connect(h.ctx(), evmConnect())
	if err != nil {
		t.Fatal(err)
	}
	if p := h.call("wc_proposeSession"); p["correlationId"] != conn.ProposalID {
		t.Fatalf("wc_proposeSession correlationId = %v, want the proposal id %d", p["correlationId"], conn.ProposalID)
	}
}

func TestTNVOnApproval(t *testing.T) {
	h := newHarness(t)
	_, _, id := h.receiveProposal(nil)
	ns := evmSession("eip155:1", "eip155:137")
	scoped := map[string]json.RawMessage{"eip155": json.RawMessage(`{"walletService":[]}`)}
	if _, err := h.e.Approve(h.ctx(), ApproveParams{ProposalID: id, Namespaces: ns,
		SessionProperties: map[string]string{"capabilities": "x"}, ScopedProperties: scoped}); err != nil {
		t.Fatal(err)
	}
	p := h.call("wc_approveSession")
	if p["correlationId"] != id {
		t.Errorf("correlationId = %v, want the proposal id %d", p["correlationId"], id)
	}
	for key, want := range map[string]string{
		"approvedChains": "[eip155:1 eip155:137]", "approvedMethods": "[personal_sign]", "approvedEvents": "[chainChanged]",
		"sessionProperties": "map[capabilities:x]",
	} {
		if got := fmt.Sprint(p[key]); got != want {
			t.Errorf("%s = %s, want %s", key, got, want)
		}
	}
	if sp, _ := p["scopedProperties"].(map[string]json.RawMessage); string(sp["eip155"]) != `{"walletService":[]}` {
		t.Errorf("scopedProperties = %v", p["scopedProperties"])
	}
}

// A dApp's session request names its chain and method, and the contract it
// calls when the first parameter carries calldata.
func TestTNVOnRequest(t *testing.T) {
	cases := []struct {
		method    string
		params    any
		contracts string
	}{
		{"eth_sendTransaction", []any{map[string]any{"to": "0xc0ffee", "data": "0x", "value": "0x1"}}, "[0xc0ffee]"},
		{"personal_sign", []any{"0x68656c6c6f", testAddress}, "<nil>"},
	}
	for _, c := range cases {
		t.Run(c.method, func(t *testing.T) {
			h := newHarness(t)
			topic, symHex := seedSessionAllowing(h, false, "personal_sign", "eth_sendTransaction")
			ctx, cancel := context.WithCancel(h.ctx())
			done := make(chan error, 1)
			go func() {
				_, err := h.e.Request(ctx, RequestParams{Topic: topic, ChainID: "eip155:1", Method: c.method, Params: raw(c.params)})
				done <- err
			}()
			for deadline := time.Now().Add(2 * time.Second); len(h.relay.publishes(topic)) == 0; {
				if time.Now().After(deadline) {
					t.Fatal("request was not published")
				}
				time.Sleep(5 * time.Millisecond)
			}
			msg, _ := h.reply(topic, symHex)
			p := h.lastPublish(topic)
			cancel()
			<-done
			if p["correlationId"] != msg.ID || fmt.Sprint(p["rpcMethods"]) != "["+c.method+"]" || p["chainId"] != "eip155:1" || fmt.Sprint(p["contractAddresses"]) != c.contracts {
				t.Fatalf("publish params %v; want correlationId %d", p, msg.ID)
			}
		})
	}
}

// A wallet's result adds the transaction hashes; an error carries nothing.
func TestTNVOnResult(t *testing.T) {
	h := newHarness(t)
	topic, _ := h.seedSession(true)
	pending := func(method string, params any) types.PendingRequest {
		req := types.PendingRequest{ID: jsonrpc.PayloadID(), Topic: topic, ChainID: "eip155:1", Method: method, Params: raw(params),
			ExpiryTimestamp: h.clock.now().Unix() + 60}
		h.update(func(tx *store.Tx) error { return tx.PutRequest(req) })
		return req
	}

	req := pending("eth_sendTransaction", []any{map[string]any{"to": "0xc0ffee", "data": "0xa9059cbb"}})
	if err := h.e.Respond(h.ctx(), topic, req.ID, raw("0xhash"), nil); err != nil {
		t.Fatal(err)
	}
	p := h.lastPublish(topic)
	if p["correlationId"] != req.ID || fmt.Sprint(p["txHashes"]) != "[0xhash]" || fmt.Sprint(p["rpcMethods"]) != "[eth_sendTransaction]" ||
		p["chainId"] != "eip155:1" || fmt.Sprint(p["contractAddresses"]) != "[0xc0ffee]" {
		t.Fatalf("result publish params %v", p)
	}

	req = pending("sui_signTransaction", map[string]any{"transaction": "x"})
	if err := h.e.Respond(h.ctx(), topic, req.ID, raw(map[string]any{"transactionBytes": suiTx, "signature": "s"}), nil); err != nil {
		t.Fatal(err)
	}
	if p := h.lastPublish(topic); fmt.Sprint(p["txHashes"]) != "["+suiTxDigest+"]" || p["contractAddresses"] != nil {
		t.Fatalf("sui result publish params %v", p)
	}

	// A method the table does not cover reports the result as text.
	req = pending("xyz_signAndSend", map[string]any{"tx": "x"})
	if err := h.e.Respond(h.ctx(), topic, req.ID, raw(map[string]any{"hash": "0xabc"}), nil); err != nil {
		t.Fatal(err)
	}
	if p := h.lastPublish(topic); fmt.Sprint(p["txHashes"]) != `[{"hash":"0xabc"}]` || fmt.Sprint(p["rpcMethods"]) != "[xyz_signAndSend]" {
		t.Fatalf("uncovered method publish params %v", p)
	}

	req = pending("eth_sendTransaction", []any{map[string]any{"to": "0xc0ffee", "data": "0x"}})
	if err := h.e.Respond(h.ctx(), topic, req.ID, nil, &jsonrpc.Error{Code: 5000, Message: "rejected"}); err != nil {
		t.Fatal(err)
	}
	p = h.lastPublish(topic)
	for _, key := range []string{"correlationId", "txHashes", "rpcMethods", "chainId", "contractAddresses"} {
		if _, ok := p[key]; ok {
			t.Errorf("error publish carries %s: %v", key, p)
		}
	}
}

// Every other request and result carries the payload id, so the relay can
// pair them: here a session update, an event, a disconnect and a handler's
// answer to a ping.
func TestTNVCorrelatesEveryMessage(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(true)
	check := func(step string) {
		t.Helper()
		msg, _ := h.reply(topic, symHex)
		if p := h.lastPublish(topic); p["correlationId"] != msg.ID {
			t.Errorf("%s: correlationId = %v, want the payload id %d", step, p["correlationId"], msg.ID)
		}
		h.relay.reset()
	}
	if _, err := h.e.Update(h.ctx(), topic, evmSession("eip155:1", "eip155:137")); err != nil {
		t.Fatal(err)
	}
	check("update")
	if err := h.e.Emit(h.ctx(), topic, "eip155:1", "chainChanged", raw("0x1")); err != nil {
		t.Fatal(err)
	}
	check("emit")
	id := jsonrpc.PayloadID()
	if !h.deliver(topic, symHex, request(id, methodPing, nil)) {
		t.Fatal("ping was not handled")
	}
	if p := h.lastPublish(topic); p["correlationId"] != id {
		t.Errorf("ping result: correlationId = %v, want the request id %d", p["correlationId"], id)
	}
	h.relay.reset()
	if err := h.e.Disconnect(h.ctx(), topic); err != nil {
		t.Fatal(err)
	}
	check("disconnect")
}
