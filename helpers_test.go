package walletconnect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/testenv"
	"github.com/WalletConnect/walletconnect-go/storage"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	chainMainnet = "eip155:1"
	chainOP      = "eip155:10"
	address      = "0xab16a96D359eC26a11e2C2b3d8f8B8942d5Bfcdb"
)

var (
	evmMethods = []string{"personal_sign", "eth_sendTransaction"}
	evmEvents  = []string{"chainChanged", "accountsChanged"}
)

type clock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.offset += d
	c.mu.Unlock()
}

type tclient struct {
	*Client
	kv    storage.KV
	clock *clock
	name  string
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newClient(t *testing.T, name string, kv storage.KV) *tclient {
	t.Helper()
	url, project := testenv.Relay(t)
	if kv == nil {
		kv = storage.NewMemory()
	}
	clk := &clock{}
	c, err := New(context.Background(), Options{
		ProjectID: project, RelayURL: url, Storage: kv, Logger: quietLogger(), now: clk.now,
		Metadata: Metadata{Name: "go-" + name, Description: "walletconnect-go test", URL: "https://example.com", Icons: []string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &tclient{Client: c, kv: kv, clock: clk, name: name}
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// next waits for the next event of type T, skipping other events.
func next[T Event](t *testing.T, c *tclient) T {
	t.Helper()
	timeout := time.After(30 * time.Second)
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				t.Fatalf("%s: events closed", c.name)
			}
			if v, ok := ev.(T); ok {
				return v
			}
		case <-timeout:
			var zero T
			t.Fatalf("%s: timed out waiting for %T", c.name, zero)
		}
	}
}

// none fails if an event of type T arrives within d.
func none[T Event](t *testing.T, c *tclient, d time.Duration) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case ev := <-c.Events():
			if v, ok := ev.(T); ok {
				t.Fatalf("%s: unexpected %T %+v", c.name, v, v)
			}
		case <-timeout:
			return
		}
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func evmProposal() map[string]ProposalNamespace {
	return map[string]ProposalNamespace{"eip155": {Chains: []string{chainMainnet, chainOP}, Methods: evmMethods, Events: evmEvents}}
}

func evmSession(chains ...string) map[string]SessionNamespace {
	var accounts []string
	for _, c := range chains {
		accounts = append(accounts, c+":"+address)
	}
	return map[string]SessionNamespace{"eip155": {Chains: chains, Accounts: accounts, Methods: evmMethods, Events: evmEvents}}
}

// connect runs the full pairing flow for EVM namespaces and returns the
// session topic.
func connect(t *testing.T, dapp, wallet *tclient) string {
	t.Helper()
	return connectWith(t, dapp, wallet, ConnectParams{
		OptionalNamespaces: evmProposal(),
		SessionProperties:  map[string]string{"k": "v"},
		ScopedProperties:   map[string]json.RawMessage{"eip155": json.RawMessage(`{"walletService":"https://example.com"}`)},
	}, evmSession(chainMainnet, chainOP))
}

// connectWith proposes p, approves it with approved and returns the session topic.
func connectWith(t *testing.T, dapp, wallet *tclient, p ConnectParams, approved map[string]SessionNamespace) string {
	t.Helper()
	ctx := ctxT(t)
	conn, err := dapp.Connect(ctx, p)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := wallet.Pair(ctx, conn.URI); err != nil {
		t.Fatalf("pair: %v", err)
	}
	prop := next[EventSessionProposal](t, wallet)
	if prop.Proposal.ID != conn.ProposalID || prop.Verification != VerificationUnknown {
		t.Fatalf("proposal = %+v", prop)
	}
	approval, err := wallet.Approve(ctx, ApproveParams{ProposalID: prop.Proposal.ID, Namespaces: approved,
		SessionProperties: prop.Proposal.SessionProperties, ScopedProperties: prop.Proposal.ScopedProperties})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	session, err := conn.Wait(ctx)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if err := approval.Acknowledged(ctx); err != nil {
		t.Fatalf("acknowledged: %v", err)
	}
	next[EventSessionConnect](t, dapp)
	if session.Topic != approval.Session.Topic {
		t.Fatalf("topics differ: dapp %s wallet %s", session.Topic, approval.Session.Topic)
	}
	return session.Topic
}

func sessionOf(t *testing.T, c *tclient, topic string) Session {
	t.Helper()
	sessions, err := c.Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.Topic == topic {
			return s
		}
	}
	t.Fatalf("%s: session %s not found", c.name, topic)
	return Session{}
}

func hasSession(c *tclient, topic string) bool {
	sessions, _ := c.Sessions(context.Background())
	for _, s := range sessions {
		if s.Topic == topic {
			return true
		}
	}
	return false
}

// craft sends a hand-built request over the relay, recording it in the
// sender's history so the peer's response can be read back with response.
func craft(t *testing.T, from *tclient, topic string, id int64, method string, params any) {
	t.Helper()
	ctx := context.Background()
	st := store.New(from.kv)
	var symHex string
	if err := st.View(ctx, func(tx *store.Tx) (err error) { symHex, err = tx.SymKey(topic); return err }); err != nil || symHex == "" {
		t.Fatalf("sym key for %s: %v", topic, err)
	}
	payload, _ := json.Marshal(jsonrpc.Request{ID: id, JSONRPC: jsonrpc.Version, Method: method, Params: params})
	if err := st.Update(ctx, func(tx *store.Tx) error {
		_, err := tx.InsertHistory(store.History{ID: id, Topic: topic, Method: method, Request: payload, Direction: store.DirectionOut})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sym, _ := crypto.DecodeKey(symHex)
	msg, _ := crypto.Encrypt(sym, payload)
	if _, err := from.relay.Request(ctx, "irn_publish", map[string]any{"topic": topic, "message": msg, "ttl": 300, "tag": 1108}); err != nil {
		t.Fatalf("publish crafted %s: %v", method, err)
	}
}

// response waits for the peer's answer to a crafted request.
func response(t *testing.T, c *tclient, id int64) *jsonrpc.Message {
	t.Helper()
	st := store.New(c.kv)
	var raw json.RawMessage
	eventually(t, fmt.Sprintf("response to %d", id), func() bool {
		_ = st.View(context.Background(), func(tx *store.Tx) error {
			h, err := tx.History(id)
			if h != nil {
				raw = h.Response
			}
			return err
		})
		return raw != nil
	})
	m, err := jsonrpc.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// restart closes c and starts a new client on the same storage, as a process
// restart would. Nothing but the storage carries over.
func restart(t *testing.T, c *tclient) *tclient {
	t.Helper()
	c.Close()
	return newClient(t, c.name, c.kv)
}

// topics returns the topics the client would subscribe to after a reconnect.
func topics(t *testing.T, c *tclient) []string {
	t.Helper()
	var out []string
	if err := store.New(c.kv).View(context.Background(), func(tx *store.Tx) (err error) {
		out, err = tx.Topics()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// requestAsync sends a request on its own goroutine and returns its result channel.
func requestAsync(ctx context.Context, c *tclient, p RequestParams) <-chan requestResult {
	out := make(chan requestResult, 1)
	go func() {
		raw, err := c.Request(ctx, p)
		out <- requestResult{raw, err}
	}()
	return out
}

type requestResult struct {
	raw json.RawMessage
	err error
}
