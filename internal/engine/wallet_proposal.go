package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/namespaces"
	"github.com/WalletConnect/walletconnect-go/internal/sdkerr"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/trace"
	"github.com/WalletConnect/walletconnect-go/internal/types"
	"github.com/WalletConnect/walletconnect-go/internal/uri"
)

// Pair joins a pairing from a wc: URI. The dApp's proposal then arrives
// through the pairing topic subscription. Pairing the same URI again before
// the proposal is answered re-emits it, as JS does; after that the URI is
// rejected with ErrPairingExists.
func (e *Engine) Pair(ctx context.Context, link string) error {
	p, err := parsePairingURI(link, e.now())
	if err != nil {
		return err
	}
	var existing *store.Pairing
	err = e.store.Update(ctx, func(tx *store.Tx) error {
		if existing, err = tx.Pairing(p.Topic); err != nil || existing != nil {
			return err
		}
		return tx.PutPairing(store.Pairing{Topic: p.Topic, SymKey: p.SymKey, Expiry: p.ExpiryTimestamp, Role: store.RoleWallet})
	})
	if err != nil {
		return err
	}
	if existing != nil {
		if existing.Role != store.RoleWallet || existing.Retired {
			return types.ErrPairingExists
		}
		return e.reemitProposals(ctx, p.Topic)
	}
	e.requestRearm()
	if err := e.relay.Subscribe(ctx, p.Topic); err != nil {
		_ = e.store.Update(context.Background(), func(tx *store.Tx) error { return tx.DeletePairing(p.Topic) })
		return fmt.Errorf("subscribe pairing topic: %w", err)
	}
	return nil
}

// parsePairingURI parses a wc: URI and fills in a default expiry.
func parsePairingURI(link string, now int64) (uri.Params, error) {
	p, err := uri.Parse(link)
	if err != nil {
		return p, fmt.Errorf("%w: %v", types.ErrInvalidParams, err)
	}
	if p.Version != 2 {
		return p, fmt.Errorf("%w: unsupported uri version %d", types.ErrInvalidParams, p.Version)
	}
	if _, err := crypto.DecodeKey(p.SymKey); err != nil {
		return p, fmt.Errorf("%w: %v", types.ErrInvalidParams, err)
	}
	if p.ExpiryTimestamp == 0 {
		p.ExpiryTimestamp = now + fiveMinutes
	}
	if p.ExpiryTimestamp < now {
		return p, types.ErrExpired
	}
	return p, nil
}

// reemitProposals emits the unanswered proposals received on a pairing again.
func (e *Engine) reemitProposals(ctx context.Context, topic string) error {
	proposals, err := e.Proposals(ctx)
	if err != nil {
		return err
	}
	for _, p := range proposals {
		if p.PairingTopic == topic && p.ExpiryTimestamp >= e.now() {
			e.emit(types.EventSessionProposal{Proposal: p, Verification: types.VerificationUnknown})
		}
	}
	return nil
}

type ApproveParams struct {
	ProposalID        int64
	Namespaces        map[string]types.SessionNamespace
	SessionProperties map[string]string
	ScopedProperties  map[string]json.RawMessage
}

type Approval struct {
	Session types.Session

	e      *Engine
	settle sentRequest
}

// Acknowledged waits for the dApp to accept the session settlement.
func (a *Approval) Acknowledged(ctx context.Context) error {
	_, err := a.e.await(ctx, a.settle, ackTimeout)
	return err
}

// Approve answers a proposal and settles the session in one relay call:
// wc_approveSession carries the proposal response on the pairing topic and
// the wc_sessionSettle request on the new session topic.
func (e *Engine) Approve(ctx context.Context, p ApproveParams) (*Approval, error) {
	prop, pairing, err := e.loadProposal(ctx, p.ProposalID)
	if err != nil {
		return nil, err
	}
	ns := namespaces.NormalizeSession(p.Namespaces)
	if err := validateApproval(prop, ns, p.ScopedProperties); err != nil {
		return nil, err
	}

	peerPub, err := crypto.DecodeKey(prop.Proposer.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: proposer public key: %v", types.ErrInvalidParams, err)
	}
	self, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	sym, err := crypto.DeriveSymKey(self.Private, peerPub)
	if err != nil {
		return nil, err
	}
	symHex := crypto.EncodeKey(sym)
	selfP := types.Participant{PublicKey: crypto.EncodeKey(self.Public), Metadata: e.meta}

	session := types.Session{
		Topic: crypto.Topic(sym), PairingTopic: prop.PairingTopic, Relay: types.Relay{Protocol: relayProtocol},
		Expiry: e.now() + sevenDays, Controller: selfP.PublicKey, Namespaces: ns,
		RequiredNamespaces: prop.RequiredNamespaces, OptionalNamespaces: prop.OptionalNamespaces,
		SessionProperties: p.SessionProperties, ScopedProperties: p.ScopedProperties,
		Self: selfP, Peer: prop.Proposer,
	}
	settleID := jsonrpc.PayloadID()
	settlePayload := requestPayload(settleID, methodSettle, settleParams{
		Relay: session.Relay, Namespaces: ns, Controller: selfP, Expiry: session.Expiry,
		SessionProperties: p.SessionProperties, ScopedProperties: p.ScopedProperties,
	})
	proposalResponse := resultPayload(prop.ID, proposeResult{Relay: session.Relay, ResponderPublicKey: selfP.PublicKey})

	err = e.store.Update(ctx, func(tx *store.Tx) error {
		if err := tx.PutSession(store.SessionRecord{Session: session, SymKey: symHex}); err != nil {
			return err
		}
		if _, err := tx.InsertHistory(e.outHistory(settleID, session.Topic, methodSettle, settlePayload)); err != nil {
			return err
		}
		return tx.SetHistoryResponse(prop.ID, proposalResponse)
	})
	if err != nil {
		return nil, err
	}

	responseMsg, err := encrypt(pairing.SymKey, proposalResponse)
	if err != nil {
		return nil, err
	}
	settleMsg, err := encrypt(symHex, settlePayload)
	if err != nil {
		return nil, err
	}
	settle := sentRequest{id: settleID, reply: e.addWaiter(ctx, settleID, session.Topic, methodSettle, ""), span: trace.From(ctx)}
	if _, err := e.relay.Request(ctx, "wc_approveSession", map[string]any{
		"pairingTopic": prop.PairingTopic, "sessionTopic": session.Topic,
		"sessionProposalResponse": responseMsg, "sessionSettlementRequest": settleMsg, "ttl": fiveMinutes,
	}); err != nil {
		e.settle(e.removeWaiter(settleID), err, "publish_failed")
		_ = e.store.Update(context.Background(), func(tx *store.Tx) error {
			if err := tx.DeleteSession(session.Topic); err != nil {
				return err
			}
			return tx.SetHistoryResponse(prop.ID, nil)
		})
		return nil, fmt.Errorf("approve session: %w", err)
	}

	trace.From(ctx).Step("approval published", "id", settleID, "sessionTopic", session.Topic)
	if err := e.store.Update(ctx, func(tx *store.Tx) error { return tx.RetirePairing(prop.PairingTopic, prop.ID) }); err != nil {
		e.log.Error("clean up approved proposal", "err", err)
	}
	e.unsubscribe(ctx, prop.PairingTopic)
	e.requestRearm()
	return &Approval{Session: session, e: e, settle: settle}, nil
}

// Reject declines a proposal with reason, or USER_REJECTED if reason is nil.
func (e *Engine) Reject(ctx context.Context, proposalID int64, reason *jsonrpc.Error) error {
	if reason == nil {
		reason = sdkerr.New(sdkerr.UserRejected, "")
	}
	prop, pairing, err := e.loadProposal(ctx, proposalID)
	if err != nil {
		return err
	}
	if err := e.publish(ctx, pairing.Topic, pairing.SymKey, errorPayload(prop.ID, reason), optsProposeReject); err != nil {
		return fmt.Errorf("reject: %w", err)
	}
	err = e.store.Update(ctx, func(tx *store.Tx) error { return tx.RetirePairing(pairing.Topic, prop.ID) })
	e.unsubscribe(ctx, pairing.Topic)
	return err
}
