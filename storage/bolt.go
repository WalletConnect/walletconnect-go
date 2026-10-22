package storage

import (
	"bytes"
	"context"
	"slices"
	"time"

	bolt "go.etcd.io/bbolt"
)

var bucket = []byte("walletconnect")

// Bolt is a KV persisted to a single bbolt file.
type Bolt struct{ db *bolt.DB }

func OpenBolt(path string) (*Bolt, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucket)
		return err
	}); err != nil {
		db.Close()
		return nil, err
	}
	return &Bolt{db: db}, nil
}

func (b *Bolt) Close() error { return b.db.Close() }

func (b *Bolt) View(ctx context.Context, fn func(Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.db.View(func(tx *bolt.Tx) error { return fn(&boltTx{b: tx.Bucket(bucket)}) })
}

func (b *Bolt) Update(ctx context.Context, fn func(Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.db.Update(func(tx *bolt.Tx) error { return fn(&boltTx{b: tx.Bucket(bucket)}) })
}

type boltTx struct{ b *bolt.Bucket }

func (t *boltTx) Get(key string) ([]byte, error) {
	v := t.b.Get([]byte(key))
	if v == nil {
		return nil, ErrNotFound
	}
	return slices.Clone(v), nil
}

func (t *boltTx) Put(key string, value []byte) error { return t.b.Put([]byte(key), value) }

func (t *boltTx) Delete(key string) error { return t.b.Delete([]byte(key)) }

func (t *boltTx) Scan(prefix string, fn func(string, []byte) error) error {
	p := []byte(prefix)
	c := t.b.Cursor()
	for k, v := c.Seek(p); k != nil && bytes.HasPrefix(k, p); k, v = c.Next() {
		if err := fn(string(k), slices.Clone(v)); err != nil {
			return err
		}
	}
	return nil
}
