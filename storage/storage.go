// Package storage defines the transactional key-value interface the SDK
// persists its state through, with in-memory and bbolt implementations.
//
// Implementations must make Update atomic and durable before it returns:
// either every write in fn is applied, or none is.
package storage

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("storage: not found")

type KV interface {
	// View runs fn in a read-only transaction.
	View(ctx context.Context, fn func(Tx) error) error
	// Update runs fn in a read-write transaction committed only if fn returns nil.
	Update(ctx context.Context, fn func(Tx) error) error
}

type Tx interface {
	// Get returns ErrNotFound when the key is missing. The returned slice is
	// owned by the caller.
	Get(key string) ([]byte, error)
	Put(key string, value []byte) error
	// Delete of a missing key is not an error.
	Delete(key string) error
	// Scan calls fn for each key with the prefix, in ascending key order.
	Scan(prefix string, fn func(key string, value []byte) error) error
}
