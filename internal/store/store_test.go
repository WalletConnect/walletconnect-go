package store

import (
	"context"
	"testing"

	"github.com/WalletConnect/walletconnect-go/internal/types"
	"github.com/WalletConnect/walletconnect-go/storage"
)

func TestStore(t *testing.T) {
	ctx := context.Background()
	s := New(storage.NewMemory())
	err := s.Update(ctx, func(tx *Tx) error {
		if err := tx.PutPairing(Pairing{Topic: "p1", SymKey: "pk", Expiry: 500, Role: RoleDApp}); err != nil {
			return err
		}
		if err := tx.PutPendingSession(PendingSession{SessionTopic: "ps", SymKey: "psk", CreatedAt: 100}); err != nil {
			return err
		}
		if err := tx.PutSession(SessionRecord{Session: types.Session{Topic: "s1", Expiry: 1000}, SymKey: "sk"}); err != nil {
			return err
		}
		if err := tx.PutRequest(types.PendingRequest{ID: 7, Topic: "s1", ExpiryTimestamp: 300}); err != nil {
			return err
		}
		ok, err := tx.InsertHistory(History{ID: 7, Topic: "s1", Method: "wc_sessionRequest"})
		if err != nil || !ok {
			t.Errorf("first insert = %v %v", ok, err)
		}
		ok, err = tx.InsertHistory(History{ID: 7, Topic: "s1", Method: "wc_sessionRequest"})
		if err != nil || ok {
			t.Errorf("duplicate insert = %v %v", ok, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	_ = s.View(ctx, func(tx *Tx) error {
		topics, _ := tx.Topics()
		if len(topics) != 3 {
			t.Errorf("topics = %v", topics)
		}
		for topic, want := range map[string]string{"s1": "sk", "ps": "psk", "p1": "pk", "nope": ""} {
			if got, _ := tx.SymKey(topic); got != want {
				t.Errorf("SymKey(%s) = %q, want %q", topic, got, want)
			}
		}
		if next, _ := tx.NextExpiry(); next != 300 {
			t.Errorf("next expiry = %d, want 300", next)
		}
		return nil
	})

	if err := s.Update(ctx, func(tx *Tx) error { return tx.DeleteSession("s1") }); err != nil {
		t.Fatal(err)
	}
	_ = s.View(ctx, func(tx *Tx) error {
		if r, _ := tx.Request(7); r != nil {
			t.Error("request survived session delete")
		}
		if h, _ := tx.History(7); h != nil {
			t.Error("history survived session delete")
		}
		return nil
	})
}

func TestForgetPairing(t *testing.T) {
	ctx := context.Background()
	s := New(storage.NewMemory())
	if err := s.Update(ctx, func(tx *Tx) error {
		if err := tx.PutPairing(Pairing{Topic: "p", SymKey: "k", Role: RoleDApp, ProposalID: 9}); err != nil {
			return err
		}
		if err := tx.PutProposal(ProposalRecord{Proposal: types.Proposal{ID: 9, PairingTopic: "p"}}); err != nil {
			return err
		}
		_, err := tx.InsertHistory(History{ID: 9, Topic: "p", Method: "wc_sessionPropose"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, func(tx *Tx) error { return tx.ForgetPairing("p", 9) }); err != nil {
		t.Fatal(err)
	}
	_ = s.View(ctx, func(tx *Tx) error {
		if p, _ := tx.Pairing("p"); p != nil {
			t.Error("pairing survived")
		}
		if p, _ := tx.Proposal(9); p != nil {
			t.Error("proposal survived")
		}
		if h, _ := tx.History(9); h != nil {
			t.Error("history survived")
		}
		return nil
	})
}
