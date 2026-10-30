package evm

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSignAndRecover(t *testing.T) {
	s, err := LoadOrCreate(filepath.Join(t.TempDir(), "key.hex"))
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"personal_sign", "eth_signTypedData_v4"} {
		params, err := RequestParams(method, s.Address.Hex(), 10)
		if err != nil {
			t.Fatal(err)
		}
		sig, err := s.Sign(method, params)
		if err != nil {
			t.Fatalf("%s: sign: %v", method, err)
		}
		got, err := Recover(method, params, sig)
		if err != nil || got != s.Address {
			t.Fatalf("%s: recovered %s, %v; want %s", method, got.Hex(), err, s.Address.Hex())
		}
	}
	params, err := RequestParams("eth_sendTransaction", s.Address.Hex(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sign("eth_sendTransaction", params); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("eth_sendTransaction: %v, want ErrUnsupported", err)
	}
}

func TestReloadKeepsAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.hex")
	a, _ := LoadOrCreate(path)
	b, err := LoadOrCreate(path)
	if err != nil || a.Address != b.Address {
		t.Fatalf("reloaded %s, %v; want %s", b.Address.Hex(), err, a.Address.Hex())
	}
}
