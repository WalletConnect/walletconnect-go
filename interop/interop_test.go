//go:build interop

// Interop tests between this SDK and the published JS SDK on the live relay.
// Run with:
//
//	cd interop/js && npm install && cd ../..
//	TEST_PROJECT_ID=... go test -tags interop ./interop/
package interop

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	wc "github.com/WalletConnect/walletconnect-go"
	"github.com/WalletConnect/walletconnect-go/internal/testenv"
)

const (
	chainMainnet = "eip155:1"
	chainOP      = "eip155:10"
	address      = "0xab16a96D359eC26a11e2C2b3d8f8B8942d5Bfcdb"
)

var (
	evmMethods = []string{"personal_sign", "eth_sendTransaction"}
	evmEvents  = []string{"chainChanged", "accountsChanged"}
)

func evmProposal() map[string]wc.ProposalNamespace {
	return map[string]wc.ProposalNamespace{"eip155": {Chains: []string{chainMainnet, chainOP}, Methods: evmMethods, Events: evmEvents}}
}

func evmSession(chains ...string) map[string]wc.SessionNamespace {
	var accounts []string
	for _, c := range chains {
		accounts = append(accounts, c+":"+address)
	}
	return map[string]wc.SessionNamespace{"eip155": {Chains: chains, Accounts: accounts, Methods: evmMethods, Events: evmEvents}}
}

// ---- JS peer ----

type jsEvent map[string]json.RawMessage

func (e jsEvent) str(key string) string {
	var s string
	_ = json.Unmarshal(e[key], &s)
	return s
}

type jsPeer struct {
	t      *testing.T
	stdin  io.WriteCloser
	events chan jsEvent
}

func startJS(t *testing.T, role string) *jsPeer {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "js")
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); err != nil {
		t.Skip("interop/js/node_modules missing; run npm install in interop/js")
	}
	cmd := exec.Command("node", "peer.mjs", role)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &jsPeer{t: t, stdin: stdin, events: make(chan jsEvent, 100)}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			var ev jsEvent
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				p.events <- ev
			}
		}
		close(p.events)
	}()
	t.Cleanup(func() {
		p.send(map[string]any{"cmd": "exit"})
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
		}
	})
	p.await("ready")
	return p
}

func (p *jsPeer) send(cmd map[string]any) {
	b, _ := json.Marshal(cmd)
	p.stdin.Write(append(b, '\n'))
}

// await returns the next event named name, failing on a JS error event.
func (p *jsPeer) await(name string) jsEvent {
	p.t.Helper()
	timeout := time.After(60 * time.Second)
	for {
		select {
		case ev, ok := <-p.events:
			if !ok {
				p.t.Fatalf("js peer exited while waiting for %s", name)
			}
			if ev.str("event") == "error" {
				p.t.Fatalf("js %s failed: %s", ev.str("cmd"), ev.str("message"))
			}
			if ev.str("event") == name {
				return ev
			}
		case <-timeout:
			p.t.Fatalf("timed out waiting for js %s", name)
		}
	}
}

// ---- Go peer ----

func newGo(t *testing.T, name string) *wc.Client {
	t.Helper()
	url, project := testenv.Relay(t)
	c, err := wc.New(context.Background(), wc.Options{
		ProjectID: project, RelayURL: url, Logger: interopLogger(),
		Metadata: wc.Metadata{Name: "go-" + name, Description: "walletconnect-go interop", URL: "https://example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func next[T wc.Event](t *testing.T, c *wc.Client) T {
	t.Helper()
	timeout := time.After(60 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if v, ok := ev.(T); ok {
				return v
			}
		case <-timeout:
			var zero T
			t.Fatalf("timed out waiting for %T", zero)
		}
	}
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// ---- matrix ----

func TestGoDAppJSWallet(t *testing.T) {
	dapp := newGo(t, "dapp")
	wallet := startJS(t, "wallet")
	ctx := ctxT(t)

	conn, err := dapp.Connect(ctx, wc.ConnectParams{OptionalNamespaces: evmProposal()})
	if err != nil {
		t.Fatal(err)
	}
	wallet.send(map[string]any{"cmd": "pair", "uri": conn.URI})
	var proposal struct {
		ID     int64 `json:"id"`
		Params struct {
			Proposer struct{ Metadata wc.Metadata } `json:"proposer"`
		} `json:"params"`
	}
	json.Unmarshal(wallet.await("session_proposal")["payload"], &proposal)
	if proposal.ID != conn.ProposalID || proposal.Params.Proposer.Metadata.Name != "go-dapp" {
		t.Fatalf("js saw proposal %+v", proposal)
	}
	wallet.send(map[string]any{"cmd": "approve", "id": proposal.ID, "namespaces": evmSession(chainMainnet, chainOP)})
	session, err := conn.Wait(ctx)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	wallet.await("acknowledged")
	topic := session.Topic
	if session.Peer.Metadata.Name != "js-wallet" {
		t.Fatalf("peer = %+v", session.Peer)
	}

	// Request with a result.
	type res struct {
		raw json.RawMessage
		err error
	}
	results := make(chan res, 1)
	request := func(method, chain string) {
		go func() {
			raw, err := dapp.Request(ctx, wc.RequestParams{Topic: topic, ChainID: chain, Method: method, Params: json.RawMessage(`["0x68656c6c6f"]`)})
			results <- res{raw, err}
		}()
	}
	var req struct {
		ID     int64  `json:"id"`
		Topic  string `json:"topic"`
		Params struct {
			ChainID string `json:"chainId"`
			Request struct {
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			} `json:"request"`
		} `json:"params"`
	}
	request("personal_sign", chainOP)
	json.Unmarshal(wallet.await("session_request")["payload"], &req)
	if req.Topic != topic || req.Params.ChainID != chainOP || req.Params.Request.Method != "personal_sign" {
		t.Fatalf("js saw request %+v", req)
	}
	wallet.send(map[string]any{"cmd": "respond", "topic": topic, "id": req.ID, "result": "0xsig"})
	if r := <-results; r.err != nil || string(r.raw) != `"0xsig"` {
		t.Fatalf("result = %s %v", r.raw, r.err)
	}

	// Request answered with an error.
	request("eth_sendTransaction", chainMainnet)
	json.Unmarshal(wallet.await("session_request")["payload"], &req)
	wallet.send(map[string]any{"cmd": "respond", "topic": topic, "id": req.ID, "error": map[string]any{"code": 5000, "message": "User rejected."}})
	var rpcErr *wc.RPCError
	if r := <-results; !errors.As(r.err, &rpcErr) || rpcErr.Code != 5000 {
		t.Fatalf("err = %v", r.err)
	}

	// Wallet-driven events and update.
	wallet.send(map[string]any{"cmd": "emit", "topic": topic, "chainId": chainMainnet, "name": "accountsChanged", "data": []string{address}})
	if ev := next[wc.EventSessionEvent](t, dapp); ev.Name != "accountsChanged" {
		t.Fatalf("event = %+v", ev)
	}
	wallet.send(map[string]any{"cmd": "update", "topic": topic, "namespaces": evmSession(chainMainnet)})
	if ev := next[wc.EventSessionUpdate](t, dapp); len(ev.Namespaces["eip155"].Accounts) != 1 {
		t.Fatalf("update = %+v", ev)
	}
	wallet.await("session_update_acked")

	// Extend and ping both ways.
	ack, err := dapp.Extend(ctx, topic)
	if err != nil {
		t.Fatalf("go extend: %v", err)
	}
	if err := ack.Wait(ctx); err != nil {
		t.Fatalf("go extend ack: %v", err)
	}
	wallet.await("session_extend")
	wallet.send(map[string]any{"cmd": "extend", "topic": topic})
	next[wc.EventSessionExtend](t, dapp)
	wallet.await("session_extend_acked")
	if err := dapp.Ping(ctx, topic); err != nil {
		t.Fatalf("go ping: %v", err)
	}
	wallet.await("session_ping")
	wallet.send(map[string]any{"cmd": "ping", "topic": topic})
	next[wc.EventSessionPing](t, dapp)
	wallet.await("pinged")

	if err := dapp.Disconnect(ctx, topic); err != nil {
		t.Fatal(err)
	}
	wallet.await("session_delete")
}

func TestJSDAppGoWallet(t *testing.T) {
	wallet := newGo(t, "wallet")
	dapp := startJS(t, "dapp")
	ctx := ctxT(t)

	dapp.send(map[string]any{"cmd": "connect", "optionalNamespaces": evmProposal()})
	if err := wallet.Pair(ctx, dapp.await("uri").str("uri")); err != nil {
		t.Fatal(err)
	}
	prop := next[wc.EventSessionProposal](t, wallet)
	if prop.Proposal.Proposer.Metadata.Name != "js-dapp" {
		t.Fatalf("proposal = %+v", prop.Proposal)
	}
	approval, err := wallet.Approve(ctx, wc.ApproveParams{ProposalID: prop.Proposal.ID, Namespaces: evmSession(chainMainnet, chainOP)})
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		Topic string                         `json:"topic"`
		Peer  struct{ Metadata wc.Metadata } `json:"peer"`
	}
	json.Unmarshal(dapp.await("session")["session"], &session)
	if err := approval.Acknowledged(ctx); err != nil {
		t.Fatalf("acknowledged: %v", err)
	}
	topic := approval.Session.Topic
	if session.Topic != topic || session.Peer.Metadata.Name != "go-wallet" {
		t.Fatalf("js session = %+v", session)
	}

	// JS request answered by Go, with a result and with an error.
	for _, tc := range []struct {
		method string
		ok     bool
	}{{"personal_sign", true}, {"eth_sendTransaction", false}} {
		dapp.send(map[string]any{"cmd": "request", "topic": topic, "chainId": chainMainnet, "method": tc.method, "params": []string{"0x68656c6c6f", address}})
		req := next[wc.EventSessionRequest](t, wallet)
		if req.Method != tc.method || req.ChainID != chainMainnet {
			t.Fatalf("go saw request %+v", req)
		}
		if tc.ok {
			err = wallet.Respond(ctx, topic, req.ID, "0xsig")
		} else {
			err = wallet.RespondError(ctx, topic, req.ID, wc.SDKError(wc.ErrKeyUserRejected, ""))
		}
		if err != nil {
			t.Fatal(err)
		}
		ev := dapp.await("request_result")
		if tc.ok && ev.str("result") != "0xsig" {
			t.Fatalf("js result = %s", ev["result"])
		}
		if !tc.ok {
			var e struct{ Code int }
			json.Unmarshal(ev["error"], &e)
			if e.Code != 5000 {
				t.Fatalf("js error = %s", ev["error"])
			}
		}
	}

	// Go-driven events and update.
	if err := wallet.Emit(ctx, topic, chainMainnet, "chainChanged", 1); err != nil {
		t.Fatal(err)
	}
	var event struct {
		Params struct {
			Event struct {
				Name string          `json:"name"`
				Data json.RawMessage `json:"data"`
			} `json:"event"`
		} `json:"params"`
	}
	json.Unmarshal(dapp.await("session_event")["payload"], &event)
	if event.Params.Event.Name != "chainChanged" || string(event.Params.Event.Data) != "1" {
		t.Fatalf("js event = %+v", event)
	}
	ack, err := wallet.Update(ctx, topic, evmSession(chainMainnet))
	if err != nil {
		t.Fatalf("go update: %v", err)
	}
	if err := ack.Wait(ctx); err != nil {
		t.Fatalf("go update ack: %v", err)
	}
	dapp.await("session_update")

	// Extend and ping both ways.
	dapp.send(map[string]any{"cmd": "extend", "topic": topic})
	next[wc.EventSessionExtend](t, wallet)
	dapp.await("session_extend_acked")
	ack, err = wallet.Extend(ctx, topic)
	if err != nil {
		t.Fatalf("go extend: %v", err)
	}
	if err := ack.Wait(ctx); err != nil {
		t.Fatalf("go extend ack: %v", err)
	}
	dapp.await("session_extend")
	dapp.send(map[string]any{"cmd": "ping", "topic": topic})
	next[wc.EventSessionPing](t, wallet)
	dapp.await("pinged")
	if err := wallet.Ping(ctx, topic); err != nil {
		t.Fatalf("go ping: %v", err)
	}
	dapp.await("session_ping")

	dapp.send(map[string]any{"cmd": "disconnect", "topic": topic})
	if ev := next[wc.EventSessionDelete](t, wallet); ev.Topic != topic {
		t.Fatalf("delete = %+v", ev)
	}
}

func TestJSAuthenticateFallbackGoWallet(t *testing.T) {
	wallet := newGo(t, "wallet")
	dapp := startJS(t, "dapp")
	ctx := ctxT(t)

	dapp.send(map[string]any{"cmd": "authenticate", "chains": []string{chainMainnet}, "methods": evmMethods})
	if err := wallet.Pair(ctx, dapp.await("uri").str("uri")); err != nil {
		t.Fatal(err)
	}
	prop := next[wc.EventSessionProposal](t, wallet)
	approval, err := wallet.Approve(ctx, wc.ApproveParams{ProposalID: prop.Proposal.ID, Namespaces: evmSession(chainMainnet)})
	if err != nil {
		t.Fatal(err)
	}
	var session struct{ Topic string }
	json.Unmarshal(dapp.await("session")["session"], &session)
	if session.Topic != approval.Session.Topic {
		t.Fatalf("js session topic %s, go %s", session.Topic, approval.Session.Topic)
	}
}

func TestJSWalletRejectsGoDApp(t *testing.T) {
	dapp := newGo(t, "dapp")
	wallet := startJS(t, "wallet")
	ctx := ctxT(t)

	conn, err := dapp.Connect(ctx, wc.ConnectParams{OptionalNamespaces: evmProposal()})
	if err != nil {
		t.Fatal(err)
	}
	wallet.send(map[string]any{"cmd": "pair", "uri": conn.URI})
	var proposal struct{ ID int64 }
	json.Unmarshal(wallet.await("session_proposal")["payload"], &proposal)
	wallet.send(map[string]any{"cmd": "reject", "id": proposal.ID})
	_, err = conn.Wait(ctx)
	var rpcErr *wc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != 5000 {
		t.Fatalf("wait err = %v, want USER_REJECTED", err)
	}
}

func interopLogger() *slog.Logger {
	if os.Getenv("INTEROP_DEBUG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
