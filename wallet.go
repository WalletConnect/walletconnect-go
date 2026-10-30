package walletconnect

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/WalletConnect/walletconnect-go/internal/uri"
)

// Pair joins a pairing from a wc: URI. The proposal arrives as an
// EventSessionProposal.
func (c *Client) Pair(ctx context.Context, link string) (err error) {
	var attrs []any
	if p, perr := uri.Parse(link); perr == nil {
		attrs = []any{"pairingTopic", p.Topic}
	}
	ctx, span := c.startTrace(ctx, "pair", attrs...)
	defer func() { span.End(err) }()
	return c.engine.Pair(ctx, link)
}

// Approve settles a session for a received proposal.
func (c *Client) Approve(ctx context.Context, p ApproveParams) (approval *Approval, err error) {
	ctx, span := c.startTrace(ctx, "approve", "proposalId", p.ProposalID)
	defer func() {
		if approval != nil {
			span.End(err, "sessionTopic", approval.Session.Topic)
		} else {
			span.End(err)
		}
	}()
	return c.engine.Approve(ctx, p)
}

// Reject declines a proposal. A nil reason sends USER_REJECTED.
func (c *Client) Reject(ctx context.Context, proposalID int64, reason *RPCError) (err error) {
	ctx, span := c.startTrace(ctx, "reject", "proposalId", proposalID)
	defer func() { span.End(err) }()
	return c.engine.Reject(ctx, proposalID, reason)
}

// Respond answers a session request with a result.
func (c *Client) Respond(ctx context.Context, topic string, id int64, result any) (err error) {
	ctx, span := c.startTrace(ctx, "respond", "topic", topic, "id", id)
	defer func() { span.End(err) }()
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return c.engine.Respond(ctx, topic, id, raw, nil)
}

// RespondError answers a session request with an error.
func (c *Client) RespondError(ctx context.Context, topic string, id int64, rpcErr *RPCError) (err error) {
	ctx, span := c.startTrace(ctx, "respond error", "topic", topic, "id", id)
	defer func() { span.End(err) }()
	if rpcErr == nil {
		return fmt.Errorf("%w: rpcErr is required", ErrInvalidParams)
	}
	return c.engine.Respond(ctx, topic, id, nil, rpcErr)
}

// Update replaces the session namespaces. It returns once the relay has
// accepted the update; Wait on the result for the dApp's ack. Only the
// controller, which is the wallet, may update.
func (c *Client) Update(ctx context.Context, topic string, namespaces map[string]SessionNamespace) (ack *Acknowledgement, err error) {
	ctx, span := c.startTrace(ctx, "update", "topic", topic)
	defer func() { span.End(err) }()
	return c.engine.Update(ctx, topic, namespaces)
}

// Emit sends a session event such as accountsChanged.
func (c *Client) Emit(ctx context.Context, topic, chainID, name string, data any) (err error) {
	ctx, span := c.startTrace(ctx, "emit", "topic", topic, "chainId", chainID, "event", name)
	defer func() { span.End(err) }()
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return c.engine.Emit(ctx, topic, chainID, name, raw)
}
