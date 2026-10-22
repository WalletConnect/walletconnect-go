// Package testvectors loads testdata/vectors.json, generated from the JS SDK
// by interop/js/gen-vectors.mjs.
package testvectors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type Vectors struct {
	Crypto struct {
		PrivA, PrivB, PubA, PubB      string
		SymKey, SymKeyReverse         string
		SessionTopic                  string
		PairingSymKey, PairingTopic   string
		IV, Plaintext, Sealed         string
		Decrypted                     string
		HashMessageInput, HashMessage string
	}
	Auth struct {
		Seed, PublicKey, ClientID, Sub, Aud string
		Iat, TTL                            int64
		JWT                                 string
		SubscriptionTopic, SubscriptionID   string
	}
	URI struct {
		Input struct {
			Topic, SymKey   string
			Version         int
			Relay           map[string]string
			ExpiryTimestamp int64
		}
		Formatted, FormattedWithMethods string
		Parsed                          []struct {
			Input, Topic, SymKey string
			Version              int
			Relay                map[string]string
			ExpiryTimestamp      int64
			Methods              []string
		}
	}
	Namespaces struct {
		Conforming []struct {
			Name      string
			Required  json.RawMessage
			Approved  json.RawMessage
			ErrorCode int
		}
		Session []struct {
			Name       string
			Namespaces json.RawMessage
			ErrorCode  int
		}
		Proposal []struct {
			Name       string
			Namespaces json.RawMessage
			ErrorCode  int
		}
	}
}

func Load(t testing.TB) *Vectors {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "testdata", "vectors.json"))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var v Vectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode vectors: %v", err)
	}
	return &v
}
