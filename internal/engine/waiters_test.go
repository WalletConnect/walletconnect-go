package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

func (h *harness) sent(topic string) sentRequest {
	id := jsonrpc.PayloadID()
	return sentRequest{id: id, reply: h.e.addWaiter(h.ctx(), id, topic, "wc_sessionRequest", "")}
}

func TestAwaitGivesUp(t *testing.T) {
	h := newHarness(t)
	if _, err := h.e.await(h.ctx(), h.sent("t"), time.Millisecond); !errors.Is(err, types.ErrExpired) {
		t.Fatalf("got %v, want ErrExpired", err)
	}
	ctx, cancel := context.WithCancel(h.ctx())
	cancel()
	if _, err := h.e.await(ctx, h.sent("t"), time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if n := h.e.Stats().Waiters; n != 0 {
		t.Fatalf("%d waiters left, want 0", n)
	}
}

func TestFailTopicOnlyFailsThatTopic(t *testing.T) {
	h := newHarness(t)
	a, b := h.sent("a"), h.sent("b")
	h.e.failTopic("a", types.ErrSessionDeleted)
	if _, err := h.e.await(h.ctx(), a, time.Second); !errors.Is(err, types.ErrSessionDeleted) {
		t.Fatalf("got %v, want ErrSessionDeleted", err)
	}
	if n := h.e.Stats().Waiters; n != 1 {
		t.Fatalf("%d waiters, want b still waiting", n)
	}
	h.e.resolve(h.ctx(), b.id, waitResult{raw: []byte("true")})
	if r, err := h.e.await(h.ctx(), b, time.Second); err != nil || string(r.raw) != "true" {
		t.Fatalf("got %s, %v", r.raw, err)
	}
	// Resolving an id nobody waits for is a no-op.
	h.e.resolve(h.ctx(), b.id, waitResult{})
}

func TestRunAcksMessagesAndFailsWaitersOnStop(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(true)
	ping := request(jsonrpc.PayloadID(), methodPing, empty{})
	payload, err := encrypt(symHex, marshal(ping))
	if err != nil {
		t.Fatal(err)
	}
	pending := h.sent(topic)

	ctx, cancel := context.WithCancel(h.ctx())
	go h.e.Run(ctx)
	h.e.Deliver(relayIncoming(topic, payload))
	for deadline := time.Now().Add(time.Second); len(eventsOf[types.EventSessionPing](h)) == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("message not processed")
		}
	}
	h.e.requestRearm()
	cancel()
	<-h.e.Done()

	h.relay.mu.Lock()
	acks := len(h.relay.acks)
	h.relay.mu.Unlock()
	if acks != 1 {
		t.Fatalf("%d acks, want 1", acks)
	}
	if _, err := h.e.await(h.ctx(), pending, time.Second); !errors.Is(err, types.ErrClosed) {
		t.Fatalf("got %v, want ErrClosed", err)
	}
	if _, err := h.e.await(h.ctx(), h.sent(topic), time.Second); !errors.Is(err, types.ErrClosed) {
		t.Fatalf("after stop got %v, want ErrClosed", err)
	}
}

func TestRunLeavesFailedMessagesUnacked(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(true)
	payload, err := encrypt(symHex, marshal(request(jsonrpc.PayloadID(), methodPing, empty{})))
	if err != nil {
		t.Fatal(err)
	}
	h.kv.failWrites.Store(true)
	ctx, cancel := context.WithCancel(h.ctx())
	go h.e.Run(ctx)
	h.e.Deliver(relayIncoming(topic, payload))
	for deadline := time.Now().Add(time.Second); h.e.Stats().QueuedEvents != 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("message not processed")
		}
	}
	cancel()
	<-h.e.Done()
	if len(h.relay.acks) != 0 {
		t.Fatal("acked a message whose handling failed")
	}
}
