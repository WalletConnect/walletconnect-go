package walletconnect

import (
	"context"

	"github.com/WalletConnect/walletconnect-go/internal/trace"
)

// startTrace opens a trace span for an API call; it is a no-op unless
// Options.Trace is set.
func (c *Client) startTrace(ctx context.Context, action string, attrs ...any) (context.Context, *trace.Span) {
	span, ctx := c.tracer.Start(ctx, action, attrs...)
	return ctx, span
}
