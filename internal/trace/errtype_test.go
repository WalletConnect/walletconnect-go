package trace

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

func TestErrorType(t *testing.T) {
	cases := map[error]string{
		fmt.Errorf("pair: %w", types.ErrExpired):                   "expired",
		fmt.Errorf("%w: bad uri", types.ErrInvalidParams):          "invalid_params",
		&jsonrpc.Error{Code: 5000, Message: "User rejected."}:      "rpc_5000",
		fmt.Errorf("approve session: %w", &jsonrpc.Error{Code: 3}): "rpc_3",
		context.DeadlineExceeded:                                   "deadline_exceeded",
		errors.New("something else"):                               "other",
	}
	for err, want := range cases {
		if got := ErrorType(err); got != want {
			t.Errorf("%v: got %q, want %q", err, got, want)
		}
	}
}

func TestEndRecordsErrorType(t *testing.T) {
	exp := &capture{}
	tr := New(Options{Exporter: exp})
	s, _ := tr.Start(context.Background(), "request")
	s.End(fmt.Errorf("wait: %w", types.ErrTimeout))
	ok, _ := tr.Start(context.Background(), "ping")
	ok.End(nil)
	if got := attrOf(exp.byName(t, "walletconnect.request").Attributes, AttrErrorType); got != "timeout" {
		t.Fatalf("error.type %v, want timeout", got)
	}
	if got := attrOf(exp.byName(t, "walletconnect.ping").Attributes, AttrErrorType); got != nil {
		t.Fatalf("a successful span has error.type %v", got)
	}
}
