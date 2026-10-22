package walletconnect

import (
	"context"
	"errors"
	"testing"

	"github.com/WalletConnect/walletconnect-go/internal/testenv"
)

func TestE2EHappyPath(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)

	d, w := sessionOf(t, dapp, topic), sessionOf(t, wallet, topic)
	if !d.Acknowledged || !w.Acknowledged {
		t.Fatalf("acknowledged dapp=%v wallet=%v", d.Acknowledged, w.Acknowledged)
	}
	if d.Controller != w.Self.PublicKey || w.Controller != w.Self.PublicKey {
		t.Fatal("controller is not the wallet")
	}
	if d.Expiry != w.Expiry || d.Peer.Metadata.Name != "go-wallet" || w.Peer.Metadata.Name != "go-dapp" {
		t.Fatalf("session mismatch dapp=%+v wallet=%+v", d, w)
	}
	if d.SessionProperties["k"] != "v" {
		t.Fatalf("session properties = %v", d.SessionProperties)
	}
	if string(d.ScopedProperties["eip155"]) != `{"walletService":"https://example.com"}` {
		t.Fatalf("scoped properties = %s", d.ScopedProperties["eip155"])
	}
	// Only the session topic is left to resubscribe: both pairings are done.
	for _, c := range []*tclient{dapp, wallet} {
		if got := topics(t, c); len(got) != 1 || got[0] != topic {
			t.Fatalf("%s topics = %v, want only the session", c.name, got)
		}
	}
	if len(d.Namespaces["eip155"].Accounts) != 2 {
		t.Fatalf("namespaces = %+v", d.Namespaces)
	}
	if props, _ := wallet.Proposals(context.Background()); len(props) != 0 {
		t.Fatalf("wallet proposals left: %v", props)
	}
	if props, _ := dapp.Proposals(context.Background()); len(props) != 0 {
		t.Fatalf("dapp proposals left: %v", props)
	}
}

func TestE2EReject(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	ctx := ctxT(t)
	conn, err := dapp.Connect(ctx, ConnectParams{OptionalNamespaces: evmProposal()})
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.Pair(ctx, conn.URI); err != nil {
		t.Fatal(err)
	}
	prop := next[EventSessionProposal](t, wallet)
	if err := wallet.Reject(ctx, prop.Proposal.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err = conn.Wait(ctx)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != 5000 {
		t.Fatalf("wait err = %v, want USER_REJECTED", err)
	}
	if err := wallet.Pair(ctx, conn.URI); !errors.Is(err, ErrPairingExists) {
		t.Fatalf("re-pair after reject err = %v, want ErrPairingExists", err)
	}
	for _, c := range []*tclient{dapp, wallet} {
		if props, _ := c.Proposals(context.Background()); len(props) != 0 {
			t.Fatalf("%s proposals left: %v", c.name, props)
		}
	}
}

func TestE2EUnknownProject(t *testing.T) {
	url, _ := testenv.Relay(t)
	c, err := New(context.Background(), Options{ProjectID: "00000000000000000000000000000000", RelayURL: url, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := ctxT(t)
	if _, err := c.Connect(ctx, ConnectParams{OptionalNamespaces: evmProposal()}); !errors.Is(err, ErrInvalidAuth) {
		t.Fatalf("connect err = %v, want ErrInvalidAuth", err)
	}
}

// Pairing the same URI again re-emits an unanswered proposal, as when a user
// closes the approval screen and scans again. Once answered, the URI is spent.
func TestE2ERepairSameURI(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	ctx := ctxT(t)
	conn, err := dapp.Connect(ctx, ConnectParams{OptionalNamespaces: evmProposal()})
	if err != nil {
		t.Fatal(err)
	}
	if err := dapp.Pair(ctx, conn.URI); !errors.Is(err, ErrPairingExists) {
		t.Fatalf("dapp pairing its own URI err = %v, want ErrPairingExists", err)
	}
	if err := wallet.Pair(ctx, conn.URI); err != nil {
		t.Fatal(err)
	}
	first := next[EventSessionProposal](t, wallet)
	if err := wallet.Pair(ctx, conn.URI); err != nil {
		t.Fatalf("second pair: %v", err)
	}
	again := next[EventSessionProposal](t, wallet)
	if again.Proposal.ID != first.Proposal.ID || again.Proposal.PairingTopic != conn.PairingTopic {
		t.Fatalf("re-emitted proposal = %+v", again.Proposal)
	}
	if _, err := wallet.Approve(ctx, ApproveParams{ProposalID: again.Proposal.ID, Namespaces: evmSession(chainMainnet)}); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := wallet.Pair(ctx, conn.URI); !errors.Is(err, ErrPairingExists) {
		t.Fatalf("pair after approve err = %v, want ErrPairingExists", err)
	}
}

// A dApp may propose without namespaces; the wallet decides what to grant.
func TestE2EConnectEmptyNamespaces(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connectWith(t, dapp, wallet, ConnectParams{}, evmSession(chainMainnet))
	if got := sessionOf(t, dapp, topic).Namespaces["eip155"].Accounts; len(got) != 1 {
		t.Fatalf("accounts = %v", got)
	}
}

// Disconnecting a pairing abandons the proposal sent on it.
func TestE2EDisconnectPairing(t *testing.T) {
	dapp := newClient(t, "dapp", nil)
	ctx := ctxT(t)
	conn, err := dapp.Connect(ctx, ConnectParams{OptionalNamespaces: evmProposal()})
	if err != nil {
		t.Fatal(err)
	}
	if err := dapp.Disconnect(ctx, conn.PairingTopic); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Wait(ctx); !errors.Is(err, ErrSessionDeleted) {
		t.Fatalf("wait err = %v, want ErrSessionDeleted", err)
	}
	if props, _ := dapp.Proposals(ctx); len(props) != 0 {
		t.Fatalf("proposals left: %+v", props)
	}
	if got := topics(t, dapp); len(got) != 0 {
		t.Fatalf("topics left: %v", got)
	}
}
