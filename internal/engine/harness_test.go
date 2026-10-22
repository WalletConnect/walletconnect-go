package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/relay"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/types"
	"github.com/WalletConnect/walletconnect-go/storage"
)

// fakeRelay records what the engine sends and answers requests with true,
// or with the error set for that method.
type fakeRelay struct {
	mu        sync.Mutex
	requests  []relayCall // Request and Enqueue
	subs      []string
	asyncSubs []string
	unsubs    []string
	acks      []relay.Incoming
	fail      map[string]error // method -> error; "irn_subscribe" covers Subscribe
}

type relayCall struct {
	method string
	params map[string]any
}

func (f *fakeRelay) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, relayCall{method, asMap(params)})
	if err := f.fail[method]; err != nil {
		return nil, err
	}
	return json.RawMessage("true"), nil
}

func (f *fakeRelay) Enqueue(_ context.Context, method string, params any, _ bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, relayCall{method, asMap(params)})
}

func (f *fakeRelay) Ack(in relay.Incoming) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acks = append(f.acks, in)
}

func (f *fakeRelay) Subscribe(_ context.Context, topic string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, topic)
	return f.fail["irn_subscribe"]
}

func (f *fakeRelay) SubscribeAsync(_ context.Context, topic string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asyncSubs = append(f.asyncSubs, topic)
}

func (f *fakeRelay) Unsubscribe(_ context.Context, topic string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unsubs = append(f.unsubs, topic)
}

func (f *fakeRelay) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = nil
}

func (f *fakeRelay) failOn(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail == nil {
		f.fail = map[string]error{}
	}
	f.fail[method] = err
}

// publishes returns the irn_publish calls on topic.
func (f *fakeRelay) publishes(topic string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.requests {
		if c.method == "irn_publish" && c.params["topic"] == topic {
			out = append(out, c.params)
		}
	}
	return out
}

func (f *fakeRelay) unsubscribed(topic string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, t := range f.unsubs {
		if t == topic {
			n++
		}
	}
	return n
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// flakyKV fails every transaction while failing is set, and only writes
// while failWrites is set.
type flakyKV struct {
	storage.KV
	failing    atomic.Bool
	failWrites atomic.Bool
}

var errStorage = errors.New("storage down")

func (k *flakyKV) View(ctx context.Context, fn func(storage.Tx) error) error {
	if k.failing.Load() {
		return errStorage
	}
	return k.KV.View(ctx, fn)
}

func (k *flakyKV) Update(ctx context.Context, fn func(storage.Tx) error) error {
	if k.failing.Load() || k.failWrites.Load() {
		return errStorage
	}
	return k.KV.Update(ctx, fn)
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type harness struct {
	t      *testing.T
	e      *Engine
	relay  *fakeRelay
	kv     *flakyKV
	store  *store.Store
	clock  *testClock
	mu     sync.Mutex
	events []types.Event
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, relay: &fakeRelay{}, kv: &flakyKV{KV: storage.NewMemory()}, clock: &testClock{t: time.Unix(1_800_000_000, 0)}}
	h.store = store.New(h.kv)
	h.e = New(Config{
		Store: h.store, Relay: h.relay, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metadata: types.Metadata{Name: "test", URL: "https://example.com"},
		Emit:     func(ev types.Event) { h.mu.Lock(); h.events = append(h.events, ev); h.mu.Unlock() },
		Now:      h.clock.now,
	})
	return h
}

func (h *harness) ctx() context.Context { return context.Background() }

// eventsOf returns the emitted events of type T.
func eventsOf[T types.Event](h *harness) []T {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []T
	for _, ev := range h.events {
		if v, ok := ev.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func (h *harness) update(fn func(*store.Tx) error) {
	h.t.Helper()
	if err := h.store.Update(h.ctx(), fn); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) view(fn func(*store.Tx) error) {
	h.t.Helper()
	if err := h.store.View(h.ctx(), fn); err != nil {
		h.t.Fatal(err)
	}
}

var nextIncomingID atomic.Int64

// deliver encrypts v for topic and runs it through the engine's handler,
// reporting whether the engine acks it.
func (h *harness) deliver(topic, symHex string, v any) bool {
	h.t.Helper()
	payload, err := json.Marshal(v)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.deliverRaw(topic, symHex, payload)
}

func (h *harness) deliverRaw(topic, symHex string, payload []byte) bool {
	h.t.Helper()
	msg, err := encrypt(symHex, payload)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.deliverMessage(topic, msg)
}

func (h *harness) deliverMessage(topic, message string) bool {
	return h.e.handle(h.ctx(), relayIncoming(topic, message))
}

// reply decodes the single message the engine published on topic.
func (h *harness) reply(topic, symHex string) (*jsonrpc.Message, int) {
	h.t.Helper()
	pubs := h.relay.publishes(topic)
	if len(pubs) != 1 {
		h.t.Fatalf("published %d messages on %s, want 1", len(pubs), short(topic))
	}
	sym, err := crypto.DecodeKey(symHex)
	if err != nil {
		h.t.Fatal(err)
	}
	plain, err := crypto.Decrypt(sym, pubs[0]["message"].(string))
	if err != nil {
		h.t.Fatal(err)
	}
	msg, err := jsonrpc.Parse(plain)
	if err != nil {
		h.t.Fatal(err)
	}
	tag, _ := pubs[0]["tag"].(int)
	return msg, tag
}

func (h *harness) noPublishes(topic string) {
	h.t.Helper()
	if n := len(h.relay.publishes(topic)); n != 0 {
		h.t.Fatalf("published %d messages on %s, want none", n, short(topic))
	}
}

func short(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

// ---- seeded state ----

const testAddress = "0xab16a96D359eC26a11e2C2b3d8f8B8942d5Bfcdb"

func evmSession(chains ...string) map[string]types.SessionNamespace {
	var accounts []string
	for _, c := range chains {
		accounts = append(accounts, c+":"+testAddress)
	}
	return map[string]types.SessionNamespace{"eip155": {
		Chains: chains, Accounts: accounts, Methods: []string{"personal_sign"}, Events: []string{"chainChanged"},
	}}
}

func randomKey(t *testing.T) crypto.Key {
	t.Helper()
	k, err := crypto.RandomKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func keyPair(t *testing.T) crypto.KeyPair {
	t.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

// seedSession stores a session; asWallet makes this client the controller.
func (h *harness) seedSession(asWallet bool) (topic, symHex string) {
	sym := randomKey(h.t)
	topic, symHex = crypto.Topic(sym), crypto.EncodeKey(sym)
	self, peer := keyPair(h.t), keyPair(h.t)
	selfP := types.Participant{PublicKey: crypto.EncodeKey(self.Public), Metadata: types.Metadata{Name: "self"}}
	peerP := types.Participant{PublicKey: crypto.EncodeKey(peer.Public), Metadata: types.Metadata{Name: "peer"}}
	controller := peerP.PublicKey
	if asWallet {
		controller = selfP.PublicKey
	}
	h.update(func(tx *store.Tx) error {
		return tx.PutSession(store.SessionRecord{Session: types.Session{
			Topic: topic, Relay: types.Relay{Protocol: "irn"}, Expiry: h.clock.now().Unix() + sevenDays,
			Acknowledged: true, Controller: controller, Namespaces: evmSession("eip155:1"),
			RequiredNamespaces: map[string]types.ProposalNamespace{}, Self: selfP, Peer: peerP,
		}, SymKey: symHex})
	})
	return topic, symHex
}

// seedWalletPairing stores a wallet-side pairing.
func (h *harness) seedWalletPairing() (topic, symHex string) {
	sym := randomKey(h.t)
	topic, symHex = crypto.Topic(sym), crypto.EncodeKey(sym)
	h.update(func(tx *store.Tx) error {
		return tx.PutPairing(store.Pairing{Topic: topic, SymKey: symHex, Expiry: h.clock.now().Unix() + fiveMinutes, Role: store.RoleWallet})
	})
	return topic, symHex
}

// seedDAppProposal stores a dApp's pairing, the proposal sent on it with its
// private key, and the proposal's history record.
func (h *harness) seedDAppProposal() (pairingTopic, symHex string, id int64, self crypto.KeyPair) {
	sym := randomKey(h.t)
	pairingTopic, symHex = crypto.Topic(sym), crypto.EncodeKey(sym)
	self = keyPair(h.t)
	id = jsonrpc.PayloadID()
	expiry := h.clock.now().Unix() + fiveMinutes
	h.update(func(tx *store.Tx) error {
		if err := tx.PutPairing(store.Pairing{Topic: pairingTopic, SymKey: symHex, Expiry: expiry, Role: store.RoleDApp, ProposalID: id}); err != nil {
			return err
		}
		if err := tx.PutProposal(store.ProposalRecord{Proposal: types.Proposal{
			ID: id, PairingTopic: pairingTopic, ExpiryTimestamp: expiry,
			Proposer:           types.Participant{PublicKey: crypto.EncodeKey(self.Public)},
			RequiredNamespaces: map[string]types.ProposalNamespace{},
		}, SelfPrivateKey: crypto.EncodeKey(self.Private)}); err != nil {
			return err
		}
		_, err := tx.InsertHistory(store.History{ID: id, Topic: pairingTopic, Method: methodPropose, Direction: store.DirectionOut})
		return err
	})
	return pairingTopic, symHex, id, self
}

func request(id int64, method string, params any) jsonrpc.Request {
	return jsonrpc.Request{ID: id, JSONRPC: jsonrpc.Version, Method: method, Params: params}
}

func result(id int64, v any) jsonrpc.Result {
	return jsonrpc.Result{ID: id, JSONRPC: jsonrpc.Version, Result: v}
}

func rpcError(id int64, code int) jsonrpc.ErrorResponse {
	return jsonrpc.ErrorResponse{ID: id, JSONRPC: jsonrpc.Version, Error: &jsonrpc.Error{Code: code, Message: "nope"}}
}

// mustReject asserts the engine published one error reply with code and tag
// on topic.
func (h *harness) mustReject(topic, symHex string, code, tag int) {
	h.t.Helper()
	msg, gotTag := h.reply(topic, symHex)
	if got := errorCode(msg); got != code || gotTag != tag {
		h.t.Fatalf("reply code %d tag %d, want code %d tag %d", got, gotTag, code, tag)
	}
}

// mustResult asserts the engine published one true result with tag on topic.
func (h *harness) mustResult(topic, symHex string, id int64, tag int) {
	h.t.Helper()
	msg, gotTag := h.reply(topic, symHex)
	if msg.Error != nil || string(msg.Result) != "true" || msg.ID != id || gotTag != tag {
		h.t.Fatalf("reply %+v tag %d, want result true for %d tag %d", msg, gotTag, id, tag)
	}
}

func (h *harness) history(id int64) *store.History {
	h.t.Helper()
	var hist *store.History
	h.view(func(tx *store.Tx) (err error) {
		hist, err = tx.History(id)
		return err
	})
	return hist
}

func (h *harness) session(topic string) *store.SessionRecord {
	h.t.Helper()
	var s *store.SessionRecord
	h.view(func(tx *store.Tx) (err error) {
		s, err = tx.Session(topic)
		return err
	})
	return s
}

func (h *harness) pairing(topic string) *store.Pairing {
	h.t.Helper()
	var p *store.Pairing
	h.view(func(tx *store.Tx) (err error) {
		p, err = tx.Pairing(topic)
		return err
	})
	return p
}

func (h *harness) proposal(id int64) *store.ProposalRecord {
	h.t.Helper()
	var p *store.ProposalRecord
	h.view(func(tx *store.Tx) (err error) {
		p, err = tx.Proposal(id)
		return err
	})
	return p
}

// received reads a result off a waiter channel without blocking forever.
func received(t *testing.T, ch chan waitResult) waitResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(time.Second):
		t.Fatal("waiter was not resolved")
		return waitResult{}
	}
}

func errorCode(msg *jsonrpc.Message) int {
	if msg.Error == nil {
		return 0
	}
	return msg.Error.Code
}

func relayIncoming(topic, message string) relay.Incoming {
	return relay.Incoming{ID: json.RawMessage(strconv.FormatInt(nextIncomingID.Add(1), 10)), Topic: topic, Message: message}
}
