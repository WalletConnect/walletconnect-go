package walletconnect

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/storage"
)

func TestE2ERestartFromStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.db")
	db, err := storage.OpenBolt(path)
	if err != nil {
		t.Fatal(err)
	}
	dapp := newClient(t, "dapp", nil)
	wallet := newClient(t, "wallet", db)
	topic := connect(t, dapp, wallet)
	ctx := ctxT(t)
	walletID := wallet.ClientID()

	wallet.Close()
	db.Close()

	results := make(chan json.RawMessage, 1)
	go func() {
		raw, err := dapp.Request(ctx, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "personal_sign"})
		if err != nil {
			t.Errorf("request: %v", err)
		}
		results <- raw
	}()
	time.Sleep(2 * time.Second)

	db, err = storage.OpenBolt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	wallet = newClient(t, "wallet", db)
	if wallet.ClientID() != walletID {
		t.Fatal("client identity changed across restart")
	}
	req := next[EventSessionRequest](t, wallet)
	if err := wallet.Respond(ctx, topic, req.ID, "0xresumed"); err != nil {
		t.Fatal(err)
	}
	if raw := <-results; string(raw) != `"0xresumed"` {
		t.Fatalf("result = %s", raw)
	}
}

// Both sides restart with a settled session: identity and session state come
// back from storage and the session still works both ways.
func TestE2EPingAfterRestart(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)
	before := map[string]Session{"dapp": sessionOf(t, dapp, topic), "wallet": sessionOf(t, wallet, topic)}
	ids := map[string]string{"dapp": dapp.ClientID(), "wallet": wallet.ClientID()}

	dapp, wallet = restart(t, dapp), restart(t, wallet)
	ctx := ctxT(t)
	for _, c := range []*tclient{dapp, wallet} {
		if c.ClientID() != ids[c.name] {
			t.Fatalf("%s: client ID changed across restart", c.name)
		}
		after := sessionOf(t, c, topic)
		if after.Expiry != before[c.name].Expiry || after.Controller != before[c.name].Controller ||
			len(after.Namespaces["eip155"].Accounts) != len(before[c.name].Namespaces["eip155"].Accounts) {
			t.Fatalf("%s: session changed across restart: %+v", c.name, after)
		}
	}
	if err := dapp.Ping(ctx, topic); err != nil {
		t.Fatalf("dapp ping after restart: %v", err)
	}
	next[EventSessionPing](t, wallet)
	if err := wallet.Ping(ctx, topic); err != nil {
		t.Fatalf("wallet ping after restart: %v", err)
	}
	next[EventSessionPing](t, dapp)
}

// The wallet restarts holding a request it has not answered yet; it can still
// answer it, and the already-acked request is not delivered again.
func TestE2ERespondToPendingRequestAfterRestart(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)
	ctx := ctxT(t)

	result := requestAsync(ctx, dapp, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "personal_sign"})
	req := next[EventSessionRequest](t, wallet)

	wallet = restart(t, wallet)
	pending, err := wallet.PendingRequests(ctx)
	if err != nil || len(pending) != 1 || pending[0].ID != req.ID || pending[0].Method != "personal_sign" {
		t.Fatalf("pending after restart = %+v, %v", pending, err)
	}
	none[EventSessionRequest](t, wallet, 2*time.Second)
	if err := wallet.Respond(ctx, topic, req.ID, "0xafter-restart"); err != nil {
		t.Fatal(err)
	}
	if r := <-result; r.err != nil || string(r.raw) != `"0xafter-restart"` {
		t.Fatalf("result = %s, %v", r.raw, r.err)
	}
}

// The dApp restarts after proposing but before the wallet pairs: the pairing,
// the proposal and the proposer key come back from storage and the session
// settles.
func TestE2EDAppRestartBeforeApproval(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	ctx := ctxT(t)
	conn, err := dapp.Connect(ctx, ConnectParams{OptionalNamespaces: evmProposal()})
	if err != nil {
		t.Fatal(err)
	}

	dapp = restart(t, dapp)
	if props, _ := dapp.Proposals(ctx); len(props) != 1 || props[0].ID != conn.ProposalID {
		t.Fatalf("dapp proposals after restart = %+v", props)
	}
	if got := topics(t, dapp); len(got) != 1 || got[0] != conn.PairingTopic {
		t.Fatalf("dapp topics after restart = %v", got)
	}
	if err := wallet.Pair(ctx, conn.URI); err != nil {
		t.Fatal(err)
	}
	prop := next[EventSessionProposal](t, wallet)
	approval, err := wallet.Approve(ctx, ApproveParams{ProposalID: prop.Proposal.ID, Namespaces: evmSession(chainMainnet)})
	if err != nil {
		t.Fatal(err)
	}
	if ev := next[EventSessionConnect](t, dapp); ev.Session.Topic != approval.Session.Topic {
		t.Fatalf("dapp settled %s, wallet %s", ev.Session.Topic, approval.Session.Topic)
	}
	if err := approval.Acknowledged(ctx); err != nil {
		t.Fatal(err)
	}
}

// The wallet restarts with a proposal on screen: it is still listed and can
// be approved.
func TestE2EWalletRestartWithPendingProposal(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	ctx := ctxT(t)
	conn, err := dapp.Connect(ctx, ConnectParams{OptionalNamespaces: evmProposal()})
	if err != nil {
		t.Fatal(err)
	}
	if err := wallet.Pair(ctx, conn.URI); err != nil {
		t.Fatal(err)
	}
	next[EventSessionProposal](t, wallet)

	wallet = restart(t, wallet)
	props, err := wallet.Proposals(ctx)
	if err != nil || len(props) != 1 || props[0].ID != conn.ProposalID || props[0].Proposer.Metadata.Name != "go-dapp" {
		t.Fatalf("wallet proposals after restart = %+v, %v", props, err)
	}
	if _, err := wallet.Approve(ctx, ApproveParams{ProposalID: conn.ProposalID, Namespaces: evmSession(chainMainnet)}); err != nil {
		t.Fatalf("approve after restart: %v", err)
	}
	if _, err := conn.Wait(ctx); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

// The dApp is offline while the wallet updates the session twice and emits
// three events. After a restart the dApp applies them from the relay mailbox
// and ends in the wallet's final state.
func TestE2EMailboxAfterRestart(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connectWith(t, dapp, wallet, ConnectParams{OptionalNamespaces: evmProposal()}, evmSession(chainMainnet))
	ctx := ctxT(t)
	dapp.Close()

	accounts := func(addrs ...string) map[string]SessionNamespace {
		var accs []string
		for _, a := range addrs {
			accs = append(accs, chainMainnet+":"+a)
		}
		return map[string]SessionNamespace{"eip155": {Chains: []string{chainMainnet}, Accounts: accs, Methods: evmMethods, Events: evmEvents}}
	}
	final := accounts(address, "0x1111111", "0x2222222")
	// The dApp is offline: each update returns once the relay has it, and the
	// wallet applies the new namespaces without waiting for the ack.
	var acks []*Acknowledgement
	for _, ns := range []map[string]SessionNamespace{accounts(address, "0x1111111"), final} {
		ack, err := wallet.Update(ctx, topic, ns)
		if err != nil {
			t.Fatalf("update while dapp offline: %v", err)
		}
		acks = append(acks, ack)
		time.Sleep(10 * time.Millisecond)
	}
	if got := sessionOf(t, wallet, topic).Namespaces["eip155"].Accounts; len(got) != 3 {
		t.Fatalf("wallet kept %v after unacked update", got)
	}
	last := []string{chainMainnet + ":0x1111111"}
	for _, data := range [][]string{last, {chainMainnet + ":0x2222222"}, last} {
		if err := wallet.Emit(ctx, topic, chainMainnet, "accountsChanged", data); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	dapp = restart(t, dapp)
	next[EventSessionUpdate](t, dapp)
	var received []string
	timeout := time.After(10 * time.Second)
collect:
	for {
		select {
		case ev := <-dapp.Events():
			if e, ok := ev.(EventSessionEvent); ok {
				received = append(received, string(e.Data))
				if len(received) == 3 {
					break collect
				}
			}
		case <-timeout:
			break collect
		}
	}
	if len(received) == 0 || received[len(received)-1] != `["`+last[0]+`"]` {
		t.Fatalf("events after restart = %v, want last %v", received, last)
	}
	eventually(t, "dapp applies the final update", func() bool {
		return len(sessionOf(t, dapp, topic).Namespaces["eip155"].Accounts) == 3
	})
	// The latest update is acknowledged once the dApp is back.
	if err := acks[len(acks)-1].Wait(ctx); err != nil {
		t.Fatalf("final update ack: %v", err)
	}
}
