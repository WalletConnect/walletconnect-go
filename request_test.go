package walletconnect

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/relay"
	"github.com/WalletConnect/walletconnect-go/internal/store"
)

func TestE2ERequestRespond(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)
	ctx := ctxT(t)

	type res struct {
		raw json.RawMessage
		err error
	}
	results := make(chan res, 1)
	go func() {
		raw, err := dapp.Request(ctx, RequestParams{Topic: topic, ChainID: chainOP, Method: "personal_sign",
			Params: json.RawMessage(`["0x68656c6c6f","` + address + `"]`)})
		results <- res{raw, err}
	}()
	req := next[EventSessionRequest](t, wallet)
	if req.Method != "personal_sign" || req.ChainID != chainOP || req.Topic != topic {
		t.Fatalf("request = %+v", req)
	}
	if pending, _ := wallet.PendingRequests(ctx); len(pending) != 1 {
		t.Fatalf("pending = %v", pending)
	}
	if err := wallet.Respond(ctx, topic, req.ID, "0xsignature"); err != nil {
		t.Fatal(err)
	}
	r := <-results
	if r.err != nil || string(r.raw) != `"0xsignature"` {
		t.Fatalf("result = %s, %v", r.raw, r.err)
	}
	if pending, _ := wallet.PendingRequests(ctx); len(pending) != 0 {
		t.Fatalf("pending after respond = %v", pending)
	}

	go func() {
		raw, err := dapp.Request(ctx, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "eth_sendTransaction", Params: json.RawMessage(`[{}]`)})
		results <- res{raw, err}
	}()
	req = next[EventSessionRequest](t, wallet)
	if err := wallet.RespondError(ctx, topic, req.ID, SDKError(ErrKeyUserRejected, "")); err != nil {
		t.Fatal(err)
	}
	r = <-results
	var rpcErr *RPCError
	if !errors.As(r.err, &rpcErr) || rpcErr.Code != 5000 {
		t.Fatalf("err = %v, want USER_REJECTED", r.err)
	}
}

func TestE2ERequestValidation(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)
	ctx := ctxT(t)

	for _, p := range []RequestParams{
		{Topic: topic, ChainID: "eip155:5", Method: "personal_sign"},
		{Topic: topic, ChainID: chainMainnet, Method: "eth_sign"},
		{Topic: topic, ChainID: chainMainnet, Method: "personal_sign", Expiry: time.Minute},
	} {
		if _, err := dapp.Request(ctx, p); !errors.Is(err, ErrInvalidParams) {
			t.Errorf("Request(%+v) err = %v, want ErrInvalidParams", p, err)
		}
	}

	id := jsonrpc.PayloadID()
	craft(t, dapp, topic, id, "wc_sessionRequest", map[string]any{
		"request": map[string]any{"method": "eth_sign", "params": []any{}}, "chainId": chainMainnet,
	})
	if m := response(t, dapp, id); m.Error == nil || m.Error.Code != 5 {
		t.Fatalf("crafted bad request response = %+v", m)
	}
	none[EventSessionRequest](t, wallet, time.Second)

	// Responding on the wrong topic, or to an unknown request, is refused.
	result := requestAsync(ctx, dapp, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "personal_sign"})
	req := next[EventSessionRequest](t, wallet)
	if err := wallet.Respond(ctx, "not-the-topic", req.ID, "0x"); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("respond on wrong topic err = %v", err)
	}
	if err := wallet.Respond(ctx, topic, req.ID+1, "0x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("respond to unknown request err = %v", err)
	}
	if err := wallet.Respond(ctx, topic, req.ID, "0x"); err != nil {
		t.Fatal(err)
	}
	<-result
}

func TestE2EDuplicateDelivery(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)
	ctx := ctxT(t)

	go dapp.Request(ctx, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "personal_sign"})
	req := next[EventSessionRequest](t, wallet)

	// Deliver the same request again, as the relay does when an ack is lost.
	st := store.New(wallet.kv)
	var symHex string
	var params json.RawMessage
	_ = st.View(ctx, func(tx *store.Tx) error {
		symHex, _ = tx.SymKey(topic)
		h, err := tx.History(req.ID)
		if h != nil {
			params = h.Request
		}
		return err
	})
	payload, _ := json.Marshal(jsonrpc.Request{ID: req.ID, JSONRPC: jsonrpc.Version, Method: "wc_sessionRequest", Params: params})
	sym, _ := crypto.DecodeKey(symHex)
	msg, _ := crypto.Encrypt(sym, payload)
	wallet.engine.Deliver(relay.Incoming{ID: json.RawMessage("1"), Topic: topic, Message: msg})
	none[EventSessionRequest](t, wallet, 2*time.Second)
}

func TestE2EReconnectMidRequest(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)
	ctx := ctxT(t)

	results := make(chan error, 1)
	go func() {
		_, err := dapp.Request(ctx, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "personal_sign"})
		results <- err
	}()
	req := next[EventSessionRequest](t, wallet)
	dapp.relay.Reconnect()
	wallet.relay.Reconnect()
	if err := wallet.Respond(ctx, topic, req.ID, "0xsig"); err != nil {
		t.Fatalf("respond across reconnect: %v", err)
	}
	if err := <-results; err != nil {
		t.Fatalf("request across reconnect: %v", err)
	}
}

// Requests work on a namespace keyed by its chain, e.g. "eip155:1".
func TestE2ERequestInlineNamespace(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	inline := map[string]SessionNamespace{chainMainnet: {Accounts: []string{chainMainnet + ":" + address}, Methods: evmMethods, Events: evmEvents}}
	topic := connectWith(t, dapp, wallet, ConnectParams{OptionalNamespaces: map[string]ProposalNamespace{
		chainMainnet: {Methods: evmMethods, Events: evmEvents},
	}}, inline)
	ctx := ctxT(t)

	result := requestAsync(ctx, dapp, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "personal_sign"})
	req := next[EventSessionRequest](t, wallet)
	if err := wallet.Respond(ctx, topic, req.ID, "0xinline"); err != nil {
		t.Fatal(err)
	}
	if r := <-result; r.err != nil {
		t.Fatal(r.err)
	}
	if err := wallet.Emit(ctx, topic, chainMainnet, "chainChanged", 1); err != nil {
		t.Fatalf("emit on inline namespace: %v", err)
	}
	next[EventSessionEvent](t, dapp)
}

// The wallet sees the expiry the dApp chose, or the 15 minute default.
func TestE2ERequestExpiry(t *testing.T) {
	dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)
	ctx := ctxT(t)

	for _, tc := range []struct {
		expiry time.Duration
		want   time.Duration
	}{{0, 15 * time.Minute}, {10 * time.Minute, 10 * time.Minute}, {24 * time.Hour, 24 * time.Hour}} {
		sent := time.Now()
		result := requestAsync(ctx, dapp, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "personal_sign", Expiry: tc.expiry})
		req := next[EventSessionRequest](t, wallet)
		got := time.Unix(req.ExpiryTimestamp, 0).Sub(sent)
		if got < tc.want-5*time.Second || got > tc.want+5*time.Second {
			t.Errorf("expiry %v: wallet saw %v, want about %v", tc.expiry, got, tc.want)
		}
		if err := wallet.Respond(ctx, topic, req.ID, "0x"); err != nil {
			t.Fatal(err)
		}
		<-result
	}
}
