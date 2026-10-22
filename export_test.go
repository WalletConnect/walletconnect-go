package walletconnect

import (
	"context"

	"github.com/WalletConnect/walletconnect-go/internal/engine"
)

// sweep runs the expiry sweep now; tests use it after moving the clock.
func (c *Client) sweep(ctx context.Context) error { return c.engine.Sweep(ctx) }

func (c *Client) engineStats() engine.Stats { return c.engine.Stats() }

func (c *Client) queuedEvents() int { return c.queue.Len() }

var engineStatsZero engine.Stats
