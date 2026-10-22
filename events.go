package walletconnect

import (
	"context"

	"github.com/WalletConnect/walletconnect-go/internal/types"
)

// Events delivered on Client.Events.
type (
	Event                = types.Event
	EventSessionProposal = types.EventSessionProposal
	EventSessionConnect  = types.EventSessionConnect
	EventSessionRequest  = types.EventSessionRequest
	EventSessionEvent    = types.EventSessionEvent
	EventSessionUpdate   = types.EventSessionUpdate
	EventSessionExtend   = types.EventSessionExtend
	EventSessionPing     = types.EventSessionPing
	EventSessionDelete   = types.EventSessionDelete
	EventSessionExpire   = types.EventSessionExpire
	EventProposalExpire  = types.EventProposalExpire
	EventRequestExpire   = types.EventRequestExpire
	EventRelayState      = types.EventRelayState
)

// Events delivers protocol events. It is closed by Close. Events are never
// dropped while the client runs, but are lost if the process exits before
// they are read; the state behind them stays queryable through Sessions,
// Proposals and PendingRequests.
func (c *Client) Events() <-chan Event { return c.events }

// dispatch moves events from the unbounded queue to the Events channel, so a
// slow reader never blocks the engine or the relay.
func (c *Client) dispatch(ctx context.Context) {
	for {
		select {
		case <-c.queue.Signal():
			for _, ev := range c.queue.PopAll() {
				select {
				case c.events <- ev:
				case <-ctx.Done():
					return
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
