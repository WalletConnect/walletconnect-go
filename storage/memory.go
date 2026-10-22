package storage

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
)

// Memory is an in-memory KV. State is lost when the process exits.
type Memory struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func NewMemory() *Memory { return &Memory{data: map[string][]byte{}} }

var errReadOnly = errors.New("storage: write in read-only transaction")

func (m *Memory) View(ctx context.Context, fn func(Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return fn(&memTx{base: m.data, readOnly: true})
}

func (m *Memory) Update(ctx context.Context, fn func(Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx := &memTx{base: m.data, writes: map[string][]byte{}, deletes: map[string]bool{}}
	if err := fn(tx); err != nil {
		return err
	}
	for k := range tx.deletes {
		delete(m.data, k)
	}
	for k, v := range tx.writes {
		m.data[k] = v
	}
	return nil
}

type memTx struct {
	base     map[string][]byte
	writes   map[string][]byte
	deletes  map[string]bool
	readOnly bool
}

func (t *memTx) Get(key string) ([]byte, error) {
	if v, ok := t.writes[key]; ok {
		return slices.Clone(v), nil
	}
	if t.deletes[key] {
		return nil, ErrNotFound
	}
	v, ok := t.base[key]
	if !ok {
		return nil, ErrNotFound
	}
	return slices.Clone(v), nil
}

func (t *memTx) Put(key string, value []byte) error {
	if t.readOnly {
		return errReadOnly
	}
	delete(t.deletes, key)
	t.writes[key] = slices.Clone(value)
	return nil
}

func (t *memTx) Delete(key string) error {
	if t.readOnly {
		return errReadOnly
	}
	delete(t.writes, key)
	t.deletes[key] = true
	return nil
}

func (t *memTx) Scan(prefix string, fn func(string, []byte) error) error {
	var keys []string
	for k := range t.base {
		if strings.HasPrefix(k, prefix) && !t.deletes[k] {
			if _, overwritten := t.writes[k]; !overwritten {
				keys = append(keys, k)
			}
		}
	}
	for k := range t.writes {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		v, err := t.Get(k)
		if err != nil {
			return err
		}
		if err := fn(k, v); err != nil {
			return err
		}
	}
	return nil
}
