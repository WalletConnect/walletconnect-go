package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

func (h *harness) sweep() {
	h.t.Helper()
	if err := h.e.Sweep(h.ctx()); err != nil {
		h.t.Fatal(err)
	}
}

func TestSweepExpiresDAppProposal(t *testing.T) {
	h := newHarness(t)
	pairingTopic, _, id, _ := h.seedDAppProposal()
	ch := h.e.addWaiter(h.ctx(), id, pairingTopic)
	h.sweep()
	if h.proposal(id) == nil {
		t.Fatal("live proposal swept")
	}

	h.clock.advance((fiveMinutes + 1) * time.Second)
	h.sweep()
	if r := received(t, ch); !errors.Is(r.err, types.ErrExpired) {
		t.Fatalf("waiter got %+v, want ErrExpired", r)
	}
	if evs := eventsOf[types.EventProposalExpire](h); len(evs) != 1 || evs[0].ProposalID != id {
		t.Fatalf("expire events %+v", evs)
	}
	if h.pairing(pairingTopic) != nil || h.proposal(id) != nil || h.relay.unsubscribed(pairingTopic) != 1 {
		t.Fatal("expired pairing left behind")
	}
}

func TestSweepExpiresReceivedProposalBeforeItsPairing(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedWalletPairing()
	p := h.proposeParams()
	p.ExpiryTimestamp = h.clock.now().Unix() + 60
	id := jsonrpc.PayloadID()
	h.deliver(topic, symHex, request(id, methodPropose, p))

	h.clock.advance(61 * time.Second)
	h.sweep()
	if evs := eventsOf[types.EventProposalExpire](h); len(evs) != 1 || evs[0].ProposalID != id {
		t.Fatalf("expire events %+v", evs)
	}
	if h.pairing(topic) == nil || h.relay.unsubscribed(topic) != 0 {
		t.Fatal("pairing swept with its proposal")
	}
}

func TestSweepDropsUnsettledPendingSession(t *testing.T) {
	h := newHarness(t)
	sessionTopic, _, _, _ := h.seedPending()
	h.clock.advance((store.PendingSessionTTL + 1) * time.Second)
	h.sweep()
	var pending *store.PendingSession
	h.view(func(tx *store.Tx) (err error) {
		pending, err = tx.PendingSession(sessionTopic)
		return err
	})
	if pending != nil || h.relay.unsubscribed(sessionTopic) != 1 {
		t.Fatal("pending session not swept")
	}
}

func TestSweepExpiresSession(t *testing.T) {
	h := newHarness(t)
	topic, symHex := h.seedSession(false)
	h.deliver(topic, symHex, request(jsonrpc.PayloadID(), methodEvent, chainChanged("eip155:1")))
	ch := h.e.addWaiter(h.ctx(), jsonrpc.PayloadID(), topic)

	h.clock.advance((sevenDays + 1) * time.Second)
	h.sweep()
	if r := received(t, ch); !errors.Is(r.err, types.ErrExpired) {
		t.Fatalf("waiter got %+v, want ErrExpired", r)
	}
	if evs := eventsOf[types.EventSessionExpire](h); len(evs) != 1 || evs[0].Topic != topic {
		t.Fatalf("expire events %+v", evs)
	}
	if h.session(topic) != nil || h.relay.unsubscribed(topic) != 1 {
		t.Fatal("expired session left behind")
	}
	if s := h.e.Stats(); s != (Stats{}) {
		t.Fatalf("stats %+v after expiry, want zero", s)
	}
}

func TestSweepExpiresRequest(t *testing.T) {
	h := newHarness(t)
	topic, _ := h.seedSession(true)
	req := types.PendingRequest{ID: jsonrpc.PayloadID(), Topic: topic, ExpiryTimestamp: h.clock.now().Unix() + 10}
	h.update(func(tx *store.Tx) error { return tx.PutRequest(req) })

	h.clock.advance(11 * time.Second)
	h.sweep()
	if evs := eventsOf[types.EventRequestExpire](h); len(evs) != 1 || evs[0].ID != req.ID {
		t.Fatalf("expire events %+v", evs)
	}
	reqs, err := h.e.PendingRequests(h.ctx())
	if err != nil || len(reqs) != 0 {
		t.Fatalf("requests %+v, %v", reqs, err)
	}
}

func TestSweepDoesNotUnsubscribeRetiredPairing(t *testing.T) {
	h := newHarness(t)
	sym := randomKey(t)
	topic := crypto.Topic(sym)
	h.update(func(tx *store.Tx) error {
		return tx.PutPairing(store.Pairing{Topic: topic, SymKey: crypto.EncodeKey(sym), Expiry: h.clock.now().Unix() + 10,
			Role: store.RoleWallet, Retired: true})
	})
	h.clock.advance(11 * time.Second)
	h.sweep()
	if h.pairing(topic) != nil {
		t.Fatal("retired pairing not swept")
	}
	if h.relay.unsubscribed(topic) != 0 {
		t.Fatal("unsubscribed a retired pairing again")
	}
}

func TestSweepPurgesOldHistory(t *testing.T) {
	h := newHarness(t)
	old, recent := jsonrpc.PayloadID(), jsonrpc.PayloadID()+1
	h.update(func(tx *store.Tx) error {
		if _, err := tx.InsertHistory(store.History{ID: old, Topic: "t", Method: methodPing, CreatedAt: h.clock.now().Unix()}); err != nil {
			return err
		}
		_, err := tx.InsertHistory(store.History{ID: recent, Topic: "t", Method: methodPing, CreatedAt: h.clock.now().Unix() + oneDay})
		return err
	})
	h.clock.advance((thirtyDays + 1) * time.Second)
	h.sweep()
	if h.history(old) != nil || h.history(recent) == nil {
		t.Fatal("history purge cut-off is wrong")
	}
}

func TestSweepReportsStorageFailure(t *testing.T) {
	h := newHarness(t)
	h.kv.failWrites.Store(true)
	if err := h.e.Sweep(h.ctx()); !errors.Is(err, errStorage) {
		t.Fatalf("got %v, want the storage error", err)
	}
}

func TestNextSweepFollowsEarliestExpiry(t *testing.T) {
	h := newHarness(t)
	if d := h.e.nextSweep(h.ctx()); d != time.Hour {
		t.Fatalf("empty store waits %v, want 1h", d)
	}
	h.seedWalletPairing()
	if d := h.e.nextSweep(h.ctx()); d != fiveMinutes*time.Second+100*time.Millisecond {
		t.Fatalf("waits %v, want the pairing expiry", d)
	}
	h.clock.advance(time.Hour)
	if d := h.e.nextSweep(h.ctx()); d != 0 {
		t.Fatalf("waits %v past an expiry, want 0", d)
	}
	h.kv.failing.Store(true)
	if d := h.e.nextSweep(h.ctx()); d != time.Hour {
		t.Fatalf("waits %v when storage fails, want 1h", d)
	}
}
