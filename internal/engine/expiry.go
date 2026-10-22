package engine

import (
	"context"

	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

// swept is what one sweep removed. Its side effects run after the store
// transaction commits.
type swept struct {
	topics    []string
	proposals []int64
	sessions  []string
	requests  []types.PendingRequest
}

type sweepStep func(tx *store.Tx, now int64, out *swept) error

// Sweep deletes everything that has expired in one transaction, then
// unsubscribes from dead topics, fails their waiters and emits the expiry
// events. It runs on the engine timer; tests call it after moving the clock.
func (e *Engine) Sweep(ctx context.Context) error {
	now := e.now()
	var out swept
	err := e.store.Update(ctx, func(tx *store.Tx) error {
		out = swept{}
		for _, step := range []sweepStep{sweepPairings, sweepProposals, sweepPendingSessions, sweepSessions, sweepRequests} {
			if err := step(tx, now, &out); err != nil {
				return err
			}
		}
		return tx.DeleteHistoryBefore(now - thirtyDays)
	})
	if err != nil {
		return err
	}
	if n := len(out.topics) + len(out.proposals) + len(out.sessions) + len(out.requests); n > 0 {
		e.tr.Event("expiry sweep", "topics", len(out.topics), "proposals", len(out.proposals),
			"sessions", len(out.sessions), "requests", len(out.requests))
	}
	e.afterSweep(ctx, out)
	return nil
}

func (e *Engine) afterSweep(ctx context.Context, out swept) {
	for _, t := range out.topics {
		e.unsubscribe(ctx, t)
	}
	for _, id := range out.proposals {
		e.resolve(ctx, id, waitResult{err: types.ErrExpired})
		e.emit(types.EventProposalExpire{ProposalID: id})
	}
	for _, t := range out.sessions {
		e.forgetTopic(t)
		e.failTopic(t, types.ErrExpired)
		e.emit(types.EventSessionExpire{Topic: t})
	}
	for _, r := range out.requests {
		e.emit(types.EventRequestExpire{Topic: r.Topic, ID: r.ID})
	}
}

// sweepPairings drops expired pairings, with the dApp proposal sent on them.
func sweepPairings(tx *store.Tx, now int64, out *swept) error {
	pairings, err := tx.Pairings()
	if err != nil {
		return err
	}
	for _, p := range pairings {
		if p.Expiry >= now {
			continue
		}
		if p.ProposalID != 0 {
			prop, err := tx.Proposal(p.ProposalID)
			if err != nil {
				return err
			}
			if prop != nil {
				out.proposals = append(out.proposals, p.ProposalID)
			}
		}
		if err := tx.ForgetPairing(p.Topic, p.ProposalID); err != nil {
			return err
		}
		if !p.Retired {
			out.topics = append(out.topics, p.Topic)
		}
	}
	return nil
}

func sweepProposals(tx *store.Tx, now int64, out *swept) error {
	proposals, err := tx.Proposals()
	if err != nil {
		return err
	}
	for _, p := range proposals {
		if p.ExpiryTimestamp == 0 || p.ExpiryTimestamp >= now {
			continue
		}
		if err := tx.DeleteProposal(p.ID); err != nil {
			return err
		}
		out.proposals = append(out.proposals, p.ID)
	}
	return nil
}

// sweepPendingSessions drops dApp session topics whose settle never came.
func sweepPendingSessions(tx *store.Tx, now int64, out *swept) error {
	pending, err := tx.PendingSessions()
	if err != nil {
		return err
	}
	for _, p := range pending {
		if p.CreatedAt+store.PendingSessionTTL >= now {
			continue
		}
		if err := tx.DeletePendingSession(p.SessionTopic); err != nil {
			return err
		}
		out.topics = append(out.topics, p.SessionTopic)
	}
	return nil
}

func sweepSessions(tx *store.Tx, now int64, out *swept) error {
	sessions, err := tx.Sessions()
	if err != nil {
		return err
	}
	for _, s := range sessions {
		if s.Expiry >= now {
			continue
		}
		if err := tx.DeleteSession(s.Topic); err != nil {
			return err
		}
		out.topics = append(out.topics, s.Topic)
		out.sessions = append(out.sessions, s.Topic)
	}
	return nil
}

func sweepRequests(tx *store.Tx, now int64, out *swept) error {
	reqs, err := tx.Requests()
	if err != nil {
		return err
	}
	for _, r := range reqs {
		if r.ExpiryTimestamp == 0 || r.ExpiryTimestamp >= now {
			continue
		}
		if err := tx.DeleteRequest(r.ID); err != nil {
			return err
		}
		out.requests = append(out.requests, r)
	}
	return nil
}
