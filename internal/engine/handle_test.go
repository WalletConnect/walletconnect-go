package engine

import (
	"encoding/base64"
	"testing"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

func TestHandleDropsWhatItCannotRead(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(true)
	other := crypto.EncodeKey(randomKey(t))
	unknown := randomKey(t)
	typeOne := base64.StdEncoding.EncodeToString(append([]byte{1}, make([]byte, 64)...))

	cases := map[string]func() bool{
		"unknown topic": func() bool {
			return h.deliver(crypto.Topic(unknown), crypto.EncodeKey(unknown), request(jsonrpc.PayloadID(), methodPing, empty{}))
		},
		"not base64":         func() bool { return h.deliverMessage(topic, "%%%") },
		"wrong key":          func() bool { return h.deliver(topic, other, request(jsonrpc.PayloadID(), methodPing, empty{})) },
		"type 1 envelope":    func() bool { return h.deliverMessage(topic, typeOne) },
		"truncated envelope": func() bool { return h.deliverMessage(topic, base64.StdEncoding.EncodeToString([]byte{0, 1, 2})) },
		"not json":           func() bool { return h.deliverRaw(topic, symHex, []byte("{")) },
		"neither kind":       func() bool { return h.deliverRaw(topic, symHex, []byte(`{"id":1,"jsonrpc":"2.0"}`)) },
		"string id":          func() bool { return h.deliverRaw(topic, symHex, []byte(`{"id":"x","method":"wc_sessionPing"}`)) },
	}
	for name, deliver := range cases {
		if !deliver() {
			t.Errorf("%s: not acked", name)
		}
	}
	h.noPublishes(topic)
	if n := len(eventsOf[types.EventSessionPing](h)); n != 0 {
		t.Fatalf("%d ping events, want 0", n)
	}
}

func TestHandleLeavesMessageUnackedWhenStorageFails(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(true)
	ping := request(jsonrpc.PayloadID(), methodPing, empty{})

	h.kv.failing.Store(true)
	if h.deliver(topic, symHex, ping) {
		t.Fatal("acked while reads fail")
	}
	h.kv.failing.Store(false)
	h.kv.failWrites.Store(true)
	if h.deliver(topic, symHex, ping) {
		t.Fatal("acked while writes fail")
	}
	h.noPublishes(topic)

	h.kv.failWrites.Store(false)
	if !h.deliver(topic, symHex, ping) {
		t.Fatal("redelivery not acked")
	}
	h.mustResult(topic, symHex, ping.ID, optsPingRes.tag)
	if n := len(eventsOf[types.EventSessionPing](h)); n != 1 {
		t.Fatalf("%d ping events, want 1", n)
	}
}

func TestHandleDropsDuplicateRequest(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(true)
	ping := request(jsonrpc.PayloadID(), methodPing, empty{})
	for range 3 {
		if !h.deliver(topic, symHex, ping) {
			t.Fatal("not acked")
		}
	}
	if n := len(h.relay.publishes(topic)); n != 1 {
		t.Fatalf("answered %d times, want once", n)
	}
	if n := len(eventsOf[types.EventSessionPing](h)); n != 1 {
		t.Fatalf("%d ping events, want 1", n)
	}
}

func TestHandleDropsUnknownAndDuplicateResponses(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(true)
	if !h.deliver(topic, symHex, result(jsonrpc.PayloadID(), true)) {
		t.Fatal("unknown response not acked")
	}

	ack, err := h.e.Extend(h.ctx(), topic)
	if err != nil {
		t.Fatal(err)
	}
	h.deliver(topic, symHex, result(ack.sent.id, true))
	if err := ack.Wait(h.ctx()); err != nil {
		t.Fatal(err)
	}
	// A redelivered response finds the history entry answered.
	if !h.deliver(topic, symHex, rpcError(ack.sent.id, 5000)) {
		t.Fatal("duplicate response not acked")
	}
	if string(h.history(ack.sent.id).Response) != string(resultPayload(ack.sent.id, true)) {
		t.Fatalf("history response overwritten: %s", h.history(ack.sent.id).Response)
	}
}

func TestHandleRecordsUnsupportedMethodsWithoutAnswering(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(true)
	for _, method := range []string{"wc_somethingNew", methodAuthenticate} {
		id := jsonrpc.PayloadID()
		if !h.deliver(topic, symHex, request(id, method, empty{})) {
			t.Fatalf("%s not acked", method)
		}
		if h.history(id) == nil {
			t.Fatalf("%s not recorded", method)
		}
	}
	h.noPublishes(topic)
}
