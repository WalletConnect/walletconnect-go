package trace

import (
	"context"
	"errors"
	"fmt"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/types"
	"github.com/WalletConnect/walletconnect-go/tracefile"
)

// AttrErrorType is the OpenTelemetry error.type attribute: a short, fixed
// class for a failed span, safe to aggregate and to report. Error messages
// can quote what a caller passed in, such as a pairing URI; this never does.
const AttrErrorType = "error.type"

var errorTypes = []struct {
	err  error
	name string
}{
	{types.ErrNotFound, "not_found"},
	{types.ErrExpired, "expired"},
	{types.ErrInvalidAuth, "invalid_auth"},
	{types.ErrTimeout, "timeout"},
	{types.ErrSessionDeleted, "session_deleted"},
	{types.ErrPairingExists, "pairing_exists"},
	{types.ErrClosed, "closed"},
	{types.ErrUnauthorized, "unauthorized"},
	{types.ErrInvalidParams, "invalid_params"},
	{context.Canceled, "canceled"},
	{context.DeadlineExceeded, "deadline_exceeded"},
}

// ErrorType classifies err: an SDK error by name, a JSON-RPC error from the
// peer or the relay as rpc_<code>, anything else as "other".
func ErrorType(err error) string {
	for _, e := range errorTypes {
		if errors.Is(err, e.err) {
			return e.name
		}
	}
	var rpc *jsonrpc.Error
	if errors.As(err, &rpc) {
		return fmt.Sprintf("rpc_%d", rpc.Code)
	}
	return "other"
}

type exporters []Exporter

func (es exporters) Export(s *tracefile.Span) error {
	var errs []error
	for _, e := range es {
		errs = append(errs, e.Export(s))
	}
	return errors.Join(errs...)
}

// Exporters sends every span to each of es; nil when es is empty.
func Exporters(es ...Exporter) Exporter {
	switch len(es) {
	case 0:
		return nil
	case 1:
		return es[0]
	}
	return exporters(es)
}
