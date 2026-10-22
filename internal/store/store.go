// Package store is the typed view of the SDK state over a storage.KV. It is
// the only durable state. Callers compose atomic operations by making several
// calls inside one Update.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/WalletConnect/walletconnect-go/storage"
)

// Key layout. Integer IDs are zero-padded by idKey so scans run in ID order.
const (
	keySeed            = "identity/seed"
	prefixPairing      = "pairing/"
	prefixProposal     = "proposal/"
	prefixPending      = "pendingsession/"
	prefixSession      = "session/"
	prefixRequest      = "request/"
	prefixHistory      = "history/"
	prefixHistoryTopic = "historytopic/"
)

type Store struct{ kv storage.KV }

func New(kv storage.KV) *Store { return &Store{kv: kv} }

type Tx struct{ tx storage.Tx }

func (s *Store) View(ctx context.Context, fn func(*Tx) error) error {
	return s.kv.View(ctx, func(t storage.Tx) error { return fn(&Tx{tx: t}) })
}

func (s *Store) Update(ctx context.Context, fn func(*Tx) error) error {
	return s.kv.Update(ctx, func(t storage.Tx) error { return fn(&Tx{tx: t}) })
}

func idKey(prefix string, id int64) string { return fmt.Sprintf("%s%020d", prefix, id) }

func (t *Tx) put(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return t.tx.Put(key, b)
}

// get decodes into v and reports whether the key existed.
func (t *Tx) get(key string, v any) (bool, error) {
	b, err := t.tx.Get(key)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}

func getPtr[T any](t *Tx, key string) (*T, error) {
	var v T
	ok, err := t.get(key, &v)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

func scan[T any](t *Tx, prefix string) ([]T, error) {
	var out []T
	err := t.tx.Scan(prefix, func(_ string, b []byte) error {
		var v T
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		out = append(out, v)
		return nil
	})
	return out, err
}
