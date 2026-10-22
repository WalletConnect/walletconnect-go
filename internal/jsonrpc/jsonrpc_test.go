package jsonrpc

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := map[string]struct {
		in   string
		want Message
	}{
		"request": {`{"id":1700000000000123,"jsonrpc":"2.0","method":"wc_sessionPing","params":{}}`,
			Message{ID: 1700000000000123, Method: "wc_sessionPing", Params: json.RawMessage(`{}`)}},
		"result":      {`{"id":2,"jsonrpc":"2.0","result":true}`, Message{ID: 2, Result: json.RawMessage(`true`)}},
		"null result": {`{"id":3,"jsonrpc":"2.0","result":null}`, Message{ID: 3, Result: json.RawMessage(`null`)}},
		"error": {`{"id":4,"jsonrpc":"2.0","error":{"code":5000,"message":"User rejected."}}`,
			Message{ID: 4, Error: &Error{Code: 5000, Message: "User rejected."}}},
		// Relay IDs exceed 2^53; they must not lose precision through float64.
		"big id": {`{"id":1700000000000123456,"result":true}`, Message{ID: 1700000000000123456, Result: json.RawMessage(`true`)}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m, err := Parse([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if m.ID != c.want.ID || m.Method != c.want.Method || string(m.Params) != string(c.want.Params) ||
				string(m.Result) != string(c.want.Result) || (m.Error == nil) != (c.want.Error == nil) {
				t.Fatalf("got %+v, want %+v", m, c.want)
			}
			if c.want.Error != nil && (m.Error.Code != c.want.Error.Code || m.Error.Message != c.want.Error.Message) {
				t.Fatalf("error %+v, want %+v", m.Error, c.want.Error)
			}
			if m.IsRequest() == m.IsResponse() {
				t.Fatalf("request %v, response %v", m.IsRequest(), m.IsResponse())
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"not json":     `{`,
		"string id":    `{"id":"x","result":true}`,
		"float id":     `{"id":1.5,"result":true}`,
		"missing id":   `{"result":true}`,
		"neither kind": `{"id":1,"jsonrpc":"2.0"}`,
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	if _, err := Parse([]byte(`{"id":1}`)); !errors.Is(err, ErrNotJSONRPC) {
		t.Fatalf("got %v, want ErrNotJSONRPC", err)
	}
}

func TestIDs(t *testing.T) {
	before := time.Now().UnixMilli()
	p, r := PayloadID(), RelayID()
	after := time.Now().UnixMilli()
	if ms := p / 1000; ms < before || ms > after {
		t.Fatalf("payload id %d is not millis*1000", p)
	}
	if ms := r / 1_000_000; ms < before || ms > after {
		t.Fatalf("relay id %d is not millis*1e6", r)
	}
	seen := map[int64]bool{}
	for range 50 {
		seen[PayloadID()] = true
	}
	if len(seen) < 2 {
		t.Fatal("payload ids have no random suffix")
	}
}

func TestErrorMessage(t *testing.T) {
	if got := (&Error{Code: 5000, Message: "User rejected."}).Error(); got != "rpc error 5000: User rejected." {
		t.Fatal(got)
	}
}
