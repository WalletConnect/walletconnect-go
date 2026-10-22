package walletconnect

import (
	"context"
	"encoding/json"
)

// Connect creates a pairing and sends a session proposal. Show Connection.URI
// to the wallet, then call Connection.Wait for the settled session.
func (c *Client) Connect(ctx context.Context, p ConnectParams) (conn *Connection, err error) {
	ctx, span := c.startTrace(ctx, "connect", "namespaces", len(p.OptionalNamespaces))
	defer func() {
		if conn != nil {
			span.End(err, "proposalId", conn.ProposalID, "pairingTopic", conn.PairingTopic)
		} else {
			span.End(err)
		}
	}()
	return c.engine.Connect(ctx, p)
}

// Request sends a session request and waits for the wallet's result. A
// wallet error is returned as *RPCError.
func (c *Client) Request(ctx context.Context, p RequestParams) (result json.RawMessage, err error) {
	ctx, span := c.startTrace(ctx, "request", "topic", p.Topic, "chainId", p.ChainID, "method", p.Method)
	defer func() { span.End(err) }()
	return c.engine.Request(ctx, p)
}
