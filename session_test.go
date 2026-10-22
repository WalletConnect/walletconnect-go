package walletconnect

import (
	"errors"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
)

func TestE2EUpdate(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)
	ctx := ctxT(t)

	ack, err := wallet.Update(ctx, topic, evmSession(chainMainnet))
	if err != nil {
		t.Fatalf("wallet update: %v", err)
	}
	if err := ack.Wait(ctx); err != nil {
		t.Fatalf("update ack: %v", err)
	}
	ev := next[EventSessionUpdate](t, dapp)
	if len(ev.Namespaces["eip155"].Accounts) != 1 {
		t.Fatalf("update event = %+v", ev)
	}
	if got := sessionOf(t, dapp, topic).Namespaces["eip155"].Accounts; len(got) != 1 {
		t.Fatalf("dapp accounts after update = %v", got)
	}
	if _, err := dapp.Update(ctx, topic, evmSession(chainMainnet)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("dapp update err = %v, want ErrUnauthorized", err)
	}

	// A crafted update from the dApp, which is not the controller.
	id := jsonrpc.PayloadID()
	craft(t, dapp, topic, id, "wc_sessionUpdate", map[string]any{"namespaces": evmSession(chainOP)})
	if m := response(t, dapp, id); m.Error == nil || m.Error.Code != 3003 {
		t.Fatalf("non-controller update response = %+v", m)
	}

	// An update older than the last one the dApp applied.
	stale := (time.Now().Add(-time.Hour).UnixMilli())*1000 + 1
	craft(t, wallet, topic, stale, "wc_sessionUpdate", map[string]any{"namespaces": evmSession(chainOP)})
	if m := response(t, wallet, stale); m.Error == nil || m.Error.Code != 1003 {
		t.Fatalf("stale update response = %+v", m)
	}
}

func TestE2EEvents(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)
	ctx := ctxT(t)

	if err := wallet.Emit(ctx, topic, chainMainnet, "accountsChanged", []string{address}); err != nil {
		t.Fatal(err)
	}
	ev := next[EventSessionEvent](t, dapp)
	if ev.Name != "accountsChanged" || ev.ChainID != chainMainnet || string(ev.Data) != `["`+address+`"]` {
		t.Fatalf("event = %+v", ev)
	}
	if err := wallet.Emit(ctx, topic, chainMainnet, "message", nil); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("emit unknown event err = %v", err)
	}

	stale := (time.Now().Add(-time.Hour).UnixMilli())*1000 + 1
	craft(t, wallet, topic, stale, "wc_sessionEvent", map[string]any{"event": map[string]any{"name": "accountsChanged", "data": []string{}}, "chainId": chainMainnet})
	none[EventSessionEvent](t, dapp, 3*time.Second)
}

func TestE2EExtendPing(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)
	ctx := ctxT(t)

	before := sessionOf(t, wallet, topic).Expiry
	wallet.clock.advance(time.Hour)
	dapp.clock.advance(time.Hour)
	ack, err := dapp.Extend(ctx, topic)
	if err != nil {
		t.Fatalf("extend: %v", err)
	}
	if err := ack.Wait(ctx); err != nil {
		t.Fatalf("extend ack: %v", err)
	}
	ev := next[EventSessionExtend](t, wallet)
	if after := sessionOf(t, wallet, topic).Expiry; after <= before || ev.Expiry != after {
		t.Fatalf("expiry before=%d after=%d event=%d", before, after, ev.Expiry)
	}

	if _, err := wallet.Extend(ctx, topic); err != nil {
		t.Fatalf("wallet extend: %v", err)
	}
	next[EventSessionExtend](t, dapp)

	if err := dapp.Ping(ctx, topic); err != nil {
		t.Fatalf("dapp ping: %v", err)
	}
	next[EventSessionPing](t, wallet)
	if err := wallet.Ping(ctx, topic); err != nil {
		t.Fatalf("wallet ping: %v", err)
	}
	next[EventSessionPing](t, dapp)
}

func TestE2EDisconnect(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)
	ctx := ctxT(t)

	if err := dapp.Disconnect(ctx, topic); err != nil {
		t.Fatal(err)
	}
	ev := next[EventSessionDelete](t, wallet)
	if ev.Topic != topic || ev.Reason == nil || ev.Reason.Code != 6000 {
		t.Fatalf("delete event = %+v", ev)
	}
	if hasSession(dapp, topic) || hasSession(wallet, topic) {
		t.Fatal("session survived disconnect")
	}
	if err := dapp.Ping(ctx, topic); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ping after disconnect err = %v", err)
	}
}
