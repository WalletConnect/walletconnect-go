package walletconnect

import "context"

// Extend pushes the session expiry 7 days out. It returns once the relay has
// accepted the request; Wait on the result for the peer's ack.
func (c *Client) Extend(ctx context.Context, topic string) (ack *Acknowledgement, err error) {
	ctx, span := c.startTrace(ctx, "extend", "topic", topic)
	defer func() { span.End(err) }()
	return c.engine.Extend(ctx, topic)
}

// Ping checks the peer is reachable and waits for its answer.
func (c *Client) Ping(ctx context.Context, topic string) (err error) {
	ctx, span := c.startTrace(ctx, "ping", "topic", topic)
	defer func() { span.End(err) }()
	return c.engine.Ping(ctx, topic)
}

// Disconnect deletes a session, telling the peer, or abandons a pairing.
func (c *Client) Disconnect(ctx context.Context, topic string) (err error) {
	ctx, span := c.startTrace(ctx, "disconnect", "topic", topic)
	defer func() { span.End(err) }()
	return c.engine.Disconnect(ctx, topic)
}

func (c *Client) Sessions(ctx context.Context) ([]Session, error) { return c.engine.Sessions(ctx) }

func (c *Client) PendingRequests(ctx context.Context) ([]PendingRequest, error) {
	return c.engine.PendingRequests(ctx)
}

func (c *Client) Proposals(ctx context.Context) ([]Proposal, error) { return c.engine.Proposals(ctx) }
