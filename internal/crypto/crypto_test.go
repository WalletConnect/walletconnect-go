package crypto

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/WalletConnect/walletconnect-go/internal/testvectors"
)

func mustKey(t *testing.T, s string) Key {
	t.Helper()
	k, err := DecodeKey(s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestVectors(t *testing.T) {
	v := testvectors.Load(t).Crypto
	privA, privB := mustKey(t, v.PrivA), mustKey(t, v.PrivB)

	pubA, err := PublicKey(privA)
	if err != nil {
		t.Fatal(err)
	}
	if got := EncodeKey(pubA); got != v.PubA {
		t.Fatalf("pubA = %s, want %s", got, v.PubA)
	}
	pubB, _ := PublicKey(privB)

	sym, err := DeriveSymKey(privA, pubB)
	if err != nil {
		t.Fatal(err)
	}
	if got := EncodeKey(sym); got != v.SymKey {
		t.Fatalf("symKey = %s, want %s", got, v.SymKey)
	}
	rev, _ := DeriveSymKey(privB, pubA)
	if EncodeKey(rev) != v.SymKeyReverse {
		t.Fatal("reverse sym key mismatch")
	}
	if got := Topic(sym); got != v.SessionTopic {
		t.Fatalf("topic = %s, want %s", got, v.SessionTopic)
	}
	if got := Topic(mustKey(t, v.PairingSymKey)); got != v.PairingTopic {
		t.Fatalf("pairing topic = %s, want %s", got, v.PairingTopic)
	}
	if got := HashMessage(v.HashMessageInput); got != v.HashMessage {
		t.Fatalf("hashMessage = %s, want %s", got, v.HashMessage)
	}

	iv, _ := hex.DecodeString(v.IV)
	sealed, err := EncryptWithIV(sym, []byte(v.Plaintext), iv)
	if err != nil {
		t.Fatal(err)
	}
	if sealed != v.Sealed {
		t.Fatalf("sealed = %s, want %s", sealed, v.Sealed)
	}
	plain, err := Decrypt(sym, v.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != v.Decrypted {
		t.Fatalf("decrypted = %s", plain)
	}
}

func TestRoundTripAndErrors(t *testing.T) {
	k, _ := RandomKey()
	msg, err := Encrypt(k, []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if p, err := Decrypt(k, msg); err != nil || string(p) != `{"a":1}` {
		t.Fatalf("round trip: %q %v", p, err)
	}
	other, _ := RandomKey()
	if _, err := Decrypt(other, msg); !errors.Is(err, ErrMalformedEnvelope) {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := Decrypt(k, "AQ=="); !errors.Is(err, ErrUnsupportedEnvelope) {
		t.Fatalf("type 1: %v", err)
	}
	if _, err := Decrypt(k, "!!!"); !errors.Is(err, ErrMalformedEnvelope) {
		t.Fatalf("garbage: %v", err)
	}
}
