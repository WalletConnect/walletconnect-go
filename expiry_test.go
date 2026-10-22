package walletconnect

import (
	"errors"
	"testing"
	"time"
)

func TestE2EExpiry(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	ctx := ctxT(t)

	// Proposal expiry.
	conn, err := dapp.Connect(ctx, ConnectParams{OptionalNamespaces: evmProposal()})
	if err != nil {
		t.Fatal(err)
	}
	dapp.clock.advance(6 * time.Minute)
	if err := dapp.sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if next[EventProposalExpire](t, dapp).ProposalID != conn.ProposalID {
		t.Fatal("wrong proposal expired")
	}
	if _, err := conn.Wait(ctx); !errors.Is(err, ErrExpired) {
		t.Fatalf("wait err = %v, want ErrExpired", err)
	}
	dapp.clock.advance(-6 * time.Minute)

	// Request expiry, then session expiry.
	topic := connect(t, dapp, wallet)
	go dapp.Request(ctx, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "personal_sign", Expiry: 5 * time.Minute})
	req := next[EventSessionRequest](t, wallet)
	wallet.clock.advance(6 * time.Minute)
	if err := wallet.sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if ev := next[EventRequestExpire](t, wallet); ev.ID != req.ID {
		t.Fatalf("expired request = %+v", ev)
	}
	for _, c := range []*tclient{dapp, wallet} {
		c.clock.advance(8 * 24 * time.Hour)
		if err := c.sweep(ctx); err != nil {
			t.Fatal(err)
		}
		if next[EventSessionExpire](t, c).Topic != topic || hasSession(c, topic) {
			t.Fatalf("%s: session did not expire", c.name)
		}
	}
}
