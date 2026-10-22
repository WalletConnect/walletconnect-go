package server

import (
	"reflect"
	"strings"
	"unicode"

	wc "github.com/WalletConnect/walletconnect-go"
)

// describe names an SDK event in snake case, e.g. EventSessionRequest becomes
// "session_request", and makes it JSON-friendly.
func describe(ev wc.Event) (string, any) {
	if e, ok := ev.(wc.EventRelayState); ok {
		out := map[string]any{"connected": e.Connected}
		if e.Err != nil {
			out["error"] = e.Err.Error()
		}
		return "relay_state", out
	}
	return snake(strings.TrimPrefix(reflect.TypeOf(ev).Name(), "Event")), ev
}

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
