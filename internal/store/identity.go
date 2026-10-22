package store

import (
	"errors"

	"github.com/WalletConnect/walletconnect-go/storage"
)

func (t *Tx) Seed() ([]byte, error) {
	b, err := t.tx.Get(keySeed)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	return b, err
}

func (t *Tx) PutSeed(seed []byte) error { return t.tx.Put(keySeed, seed) }
