package uri

import (
	"errors"
	"reflect"
	"testing"

	"github.com/WalletConnect/walletconnect-go/internal/testvectors"
)

func TestVectors(t *testing.T) {
	v := testvectors.Load(t).URI
	p := Params{Topic: v.Input.Topic, Version: v.Input.Version, SymKey: v.Input.SymKey, Relay: v.Input.Relay, ExpiryTimestamp: v.Input.ExpiryTimestamp}
	if got := Format(p); got != v.Formatted {
		t.Fatalf("format = %s, want %s", got, v.Formatted)
	}
	p.Methods = []string{"wc_sessionPropose", "wc_sessionAuthenticate"}
	if got := Format(p); got != v.FormattedWithMethods {
		t.Fatalf("format methods = %s, want %s", got, v.FormattedWithMethods)
	}
	for _, c := range v.Parsed {
		got, err := Parse(c.Input)
		if err != nil {
			t.Fatalf("parse %s: %v", c.Input, err)
		}
		if got.Topic != c.Topic || got.Version != c.Version || got.SymKey != c.SymKey || got.ExpiryTimestamp != c.ExpiryTimestamp {
			t.Fatalf("parse %s = %+v, want %+v", c.Input, got, c)
		}
		if !reflect.DeepEqual(got.Relay, c.Relay) {
			t.Fatalf("relay = %v, want %v", got.Relay, c.Relay)
		}
		if len(got.Methods) != len(c.Methods) || (len(c.Methods) > 0 && !reflect.DeepEqual(got.Methods, c.Methods)) {
			t.Fatalf("methods = %v, want %v", got.Methods, c.Methods)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"", "http://x", "wc:@2?symKey=a", "wc:abc@x?symKey=a"} {
		if _, err := Parse(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) err = %v, want ErrInvalid", s, err)
		}
	}
}
