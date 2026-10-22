package walletconnect

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/uri"
)

// newOfflineClient never contacts the relay: every call under test fails
// validation first, so these tests need no project ID.
func newOfflineClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(context.Background(), Options{ProjectID: "offline", RelayURL: "wss://127.0.0.1:1", Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func pairingURI(t *testing.T, version int, expiry int64) string {
	k, _ := crypto.RandomKey()
	return uri.Format(uri.Params{Topic: crypto.Topic(k), Version: version, SymKey: crypto.EncodeKey(k),
		Relay: map[string]string{"protocol": "irn"}, ExpiryTimestamp: expiry})
}

func TestNewRequiresProjectID(t *testing.T) {
	if _, err := New(context.Background(), Options{}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("err = %v, want ErrInvalidParams", err)
	}
}

func TestValidation(t *testing.T) {
	c := newOfflineClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const topic = "0000000000000000000000000000000000000000000000000000000000000000"

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"connect: namespace without chains", func() error {
			_, err := c.Connect(ctx, ConnectParams{OptionalNamespaces: map[string]ProposalNamespace{"eip155": {Methods: evmMethods}}})
			return err
		}, ErrInvalidParams},
		{"connect: malformed chain", func() error {
			_, err := c.Connect(ctx, ConnectParams{OptionalNamespaces: map[string]ProposalNamespace{"eip155": {Chains: []string{"eip155"}}}})
			return err
		}, ErrInvalidParams},
		{"connect: scoped properties outside namespaces", func() error {
			_, err := c.Connect(ctx, ConnectParams{OptionalNamespaces: evmProposal(), ScopedProperties: map[string]json.RawMessage{"solana": json.RawMessage(`{}`)}})
			return err
		}, ErrInvalidParams},
		{"pair: not a wc uri", func() error { return c.Pair(ctx, "https://example.com") }, ErrInvalidParams},
		{"pair: version 1", func() error { return c.Pair(ctx, pairingURI(t, 1, 0)) }, ErrInvalidParams},
		{"pair: bad symKey", func() error {
			return c.Pair(ctx, strings.Replace(pairingURI(t, 2, 0), "symKey=", "symKey=zz", 1))
		}, ErrInvalidParams},
		{"pair: expired", func() error { return c.Pair(ctx, pairingURI(t, 2, time.Now().Add(-time.Minute).Unix())) }, ErrExpired},
		{"approve: unknown proposal", func() error {
			_, err := c.Approve(ctx, ApproveParams{ProposalID: 1, Namespaces: evmSession(chainMainnet)})
			return err
		}, ErrNotFound},
		{"reject: unknown proposal", func() error { return c.Reject(ctx, 1, nil) }, ErrNotFound},
		{"request: unknown session", func() error {
			_, err := c.Request(ctx, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "personal_sign"})
			return err
		}, ErrNotFound},
		{"respond: unknown request", func() error { return c.Respond(ctx, topic, 1, "0x") }, ErrNotFound},
		{"respond: nil error", func() error { return c.RespondError(ctx, topic, 1, nil) }, ErrInvalidParams},
		{"update: unknown session", func() error { _, err := c.Update(ctx, topic, evmSession(chainMainnet)); return err }, ErrNotFound},
		{"emit: unknown session", func() error { return c.Emit(ctx, topic, chainMainnet, "chainChanged", 1) }, ErrNotFound},
		{"extend: unknown session", func() error { _, err := c.Extend(ctx, topic); return err }, ErrNotFound},
		{"ping: unknown session", func() error { return c.Ping(ctx, topic) }, ErrNotFound},
		{"disconnect: unknown topic", func() error { return c.Disconnect(ctx, topic) }, ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
