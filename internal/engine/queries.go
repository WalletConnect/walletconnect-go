package engine

import (
	"context"
	"fmt"

	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

func (e *Engine) Sessions(ctx context.Context) ([]types.Session, error) {
	var out []types.Session
	err := e.store.View(ctx, func(tx *store.Tx) error {
		recs, err := tx.Sessions()
		for _, r := range recs {
			out = append(out, r.Session)
		}
		return err
	})
	return out, err
}

func (e *Engine) PendingRequests(ctx context.Context) ([]types.PendingRequest, error) {
	var out []types.PendingRequest
	err := e.store.View(ctx, func(tx *store.Tx) (err error) {
		out, err = tx.Requests()
		return err
	})
	return out, err
}

func (e *Engine) Proposals(ctx context.Context) ([]types.Proposal, error) {
	var out []types.Proposal
	err := e.store.View(ctx, func(tx *store.Tx) error {
		recs, err := tx.Proposals()
		for _, r := range recs {
			out = append(out, r.Proposal)
		}
		return err
	})
	return out, err
}

// loadSession returns the session or ErrNotFound.
func (e *Engine) loadSession(ctx context.Context, topic string) (*store.SessionRecord, error) {
	var s *store.SessionRecord
	err := e.store.View(ctx, func(tx *store.Tx) (err error) {
		s, err = tx.Session(topic)
		return err
	})
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("session %s: %w", topic, types.ErrNotFound)
	}
	return s, nil
}

// loadProposal returns a live proposal and its pairing, or ErrNotFound or
// ErrExpired.
func (e *Engine) loadProposal(ctx context.Context, id int64) (*store.ProposalRecord, *store.Pairing, error) {
	var prop *store.ProposalRecord
	var pairing *store.Pairing
	err := e.store.View(ctx, func(tx *store.Tx) (err error) {
		if prop, err = tx.Proposal(id); err != nil || prop == nil {
			return err
		}
		pairing, err = tx.Pairing(prop.PairingTopic)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	if prop == nil || pairing == nil {
		return nil, nil, fmt.Errorf("proposal %d: %w", id, types.ErrNotFound)
	}
	if prop.ExpiryTimestamp != 0 && prop.ExpiryTimestamp < e.now() {
		return nil, nil, fmt.Errorf("proposal %d: %w", id, types.ErrExpired)
	}
	return prop, pairing, nil
}
