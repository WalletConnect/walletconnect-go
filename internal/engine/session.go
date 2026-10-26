package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/sdkerr"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

const ackTimeout = fiveMinutes * time.Second

// Extend moves the session expiry 7 days out. It returns once the relay has
// accepted the request; wait on the result for the peer's ack.
func (e *Engine) Extend(ctx context.Context, topic string) (*Acknowledgement, error) {
	s, err := e.loadSession(ctx, topic)
	if err != nil {
		return nil, err
	}
	expiry := e.now() + sevenDays
	sent, err := e.sendRequest(ctx, s, methodExtend, empty{}, optsExtendReq, func(tx *store.Tx) error {
		cur, err := tx.Session(topic)
		if err != nil || cur == nil {
			return err
		}
		cur.Expiry = expiry
		return tx.PutSession(*cur)
	})
	e.requestRearm()
	if err != nil {
		return nil, err
	}
	return &Acknowledgement{e: e, sent: sent}, nil
}

// Ping waits for the peer to answer a ping.
func (e *Engine) Ping(ctx context.Context, topic string) error {
	s, err := e.loadSession(ctx, topic)
	if err != nil {
		return err
	}
	sent, err := e.sendRequest(ctx, s, methodPing, empty{}, optsPingReq, nil)
	if err != nil {
		return err
	}
	_, err = e.await(ctx, sent, ackTimeout)
	return err
}

// Disconnect deletes a session after telling the peer, or abandons a pairing.
func (e *Engine) Disconnect(ctx context.Context, topic string) error {
	var s *store.SessionRecord
	var pairing *store.Pairing
	if err := e.store.View(ctx, func(tx *store.Tx) (err error) {
		if s, err = tx.Session(topic); err != nil || s != nil {
			return err
		}
		pairing, err = tx.Pairing(topic)
		return err
	}); err != nil {
		return err
	}

	var forget func(*store.Tx) error
	switch {
	case s != nil:
		id := jsonrpc.PayloadID()
		payload := requestPayload(id, methodDelete, sdkerr.New(sdkerr.UserDisconnected, ""))
		if err := e.publish(ctx, topic, s.SymKey, payload, optsDeleteReq.with(correlate(id))); err != nil {
			return err
		}
		forget = func(tx *store.Tx) error { return tx.DeleteSession(topic) }
	case pairing != nil:
		forget = func(tx *store.Tx) error { return tx.ForgetPairing(topic, pairing.ProposalID) }
	default:
		return fmt.Errorf("topic %s: %w", topic, types.ErrNotFound)
	}
	if err := e.store.Update(ctx, forget); err != nil {
		return err
	}
	e.unsubscribe(ctx, topic)
	e.forgetTopic(topic)
	e.failTopic(topic, types.ErrSessionDeleted)
	return nil
}
