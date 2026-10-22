package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func testKV(t *testing.T, kv KV) {
	ctx := context.Background()
	if err := kv.Update(ctx, func(tx Tx) error {
		for _, k := range []string{"a/2", "a/1", "b/1"} {
			if err := tx.Put(k, []byte(k)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	if err := kv.Update(ctx, func(tx Tx) error {
		_ = tx.Put("a/3", []byte("x"))
		_ = tx.Delete("a/1")
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("rollback err = %v", err)
	}

	if err := kv.Update(ctx, func(tx Tx) error {
		if err := tx.Delete("b/1"); err != nil {
			return err
		}
		if _, err := tx.Get("b/1"); !errors.Is(err, ErrNotFound) {
			t.Errorf("deleted key visible in tx: %v", err)
		}
		return tx.Put("a/0", []byte("zero"))
	}); err != nil {
		t.Fatal(err)
	}

	var keys []string
	if err := kv.View(ctx, func(tx Tx) error {
		if _, err := tx.Get("a/3"); !errors.Is(err, ErrNotFound) {
			t.Errorf("rolled back write is visible: %v", err)
		}
		if _, err := tx.Get("b/1"); !errors.Is(err, ErrNotFound) {
			t.Errorf("deleted key is visible: %v", err)
		}
		return tx.Scan("a/", func(k string, v []byte) error {
			keys = append(keys, k)
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"a/0", "a/1", "a/2"}
	if len(keys) != len(want) {
		t.Fatalf("scan = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("scan = %v, want %v", keys, want)
		}
	}
}

func TestMemory(t *testing.T) { testKV(t, NewMemory()) }

func TestBolt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wc.db")
	b, err := OpenBolt(path)
	if err != nil {
		t.Fatal(err)
	}
	testKV(t, b)
	b.Close()

	b, err = OpenBolt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.View(context.Background(), func(tx Tx) error {
		v, err := tx.Get("a/0")
		if err != nil || string(v) != "zero" {
			t.Errorf("after reopen: %q %v", v, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
