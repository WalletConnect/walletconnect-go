package walletconnect

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WalletConnect/walletconnect-go/internal/uri"
	"github.com/WalletConnect/walletconnect-go/storage"
	"github.com/WalletConnect/walletconnect-go/tracefile"
)

func openBolt(t *testing.T, dir string) *storage.Bolt {
	t.Helper()
	db, err := storage.OpenBolt(filepath.Join(dir, "wallet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newTraceClient never contacts the relay; it only exercises Options.
func newTraceClient(t *testing.T, opts Options) *Client {
	t.Helper()
	opts.ProjectID, opts.RelayURL, opts.Logger = "offline", "wss://127.0.0.1:1", quietLogger()
	c, err := New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// With a bbolt storage the trace file appears beside the database without
// any option, and a span ended before Close is in it.
func TestTraceFileBesideDatabase(t *testing.T) {
	dir := t.TempDir()
	c := newTraceClient(t, Options{Storage: openBolt(t, dir), Metadata: Metadata{Name: "test-wallet"}})
	if c.traceFile == nil {
		t.Fatal("trace file should be on with a bbolt storage")
	}
	c.tracer.Event("probe", "topic", "abc")
	c.Close()

	path := filepath.Join(dir, "wallet.trace.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var names []string
	for s, err := range tracefile.Read(f) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, s.Name)
		if s.Name == "walletconnect.probe" && s.Attr("wc.topic") != "abc" {
			t.Errorf("probe span %+v", s)
		}
	}
	if len(names) != 1 || names[0] != "walletconnect.probe" {
		t.Fatalf("file holds %v", names)
	}
	if c.traceFile.Export(&tracefile.Span{}) == nil {
		t.Fatal("the trace file should be closed with the client")
	}
}

// A pair span names its pairing topic, even when the pair fails, so the
// trace and telemetry can be tied to the pairing; the URI itself never is.
func TestTraceFilePairTopic(t *testing.T) {
	dir := t.TempDir()
	c := newTraceClient(t, Options{Storage: openBolt(t, dir)})
	link := pairingURI(t, 2, 1)
	p, _ := uri.Parse(link)
	if err := c.Pair(context.Background(), link); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
	c.Close()

	data, err := os.ReadFile(filepath.Join(dir, "wallet.trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), p.SymKey) {
		t.Fatal("trace file holds the symKey")
	}
	for s, err := range tracefile.Read(bytes.NewReader(data)) {
		if err != nil {
			t.Fatal(err)
		}
		if s.Name == "walletconnect.pair" {
			if got := s.Attr("wc.pairing_topic"); got != p.Topic {
				t.Fatalf("pairing topic = %q, want %q", got, p.Topic)
			}
			return
		}
	}
	t.Fatal("no pair span")
}

func TestTraceFileOptions(t *testing.T) {
	t.Run("memory storage has none", func(t *testing.T) {
		if c := newTraceClient(t, Options{}); c.traceFile != nil {
			t.Fatal("memory storage should have no trace file")
		}
	})
	t.Run("NoTraceFile", func(t *testing.T) {
		dir := t.TempDir()
		c := newTraceClient(t, Options{Storage: openBolt(t, dir), NoTraceFile: true})
		if c.traceFile != nil {
			t.Fatal("NoTraceFile should turn it off")
		}
		if _, err := os.Stat(filepath.Join(dir, "wallet.trace.jsonl")); err == nil {
			t.Fatal("no file should be created")
		}
	})
	t.Run("explicit path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "custom.jsonl")
		c := newTraceClient(t, Options{TraceFile: path})
		if c.traceFile == nil || c.traceFile.Path() != path {
			t.Fatalf("trace file %v, want %s", c.traceFile, path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("explicit path that cannot open fails New", func(t *testing.T) {
		_, err := New(context.Background(), Options{ProjectID: "offline", RelayURL: "wss://127.0.0.1:1", Logger: quietLogger(),
			TraceFile: filepath.Join(t.TempDir(), "missing", "dir", "t.jsonl")})
		if err == nil {
			t.Fatal("New should fail")
		}
	})
	t.Run("default path that cannot open is skipped", func(t *testing.T) {
		dir := t.TempDir()
		db := openBolt(t, dir)
		// Occupy the trace file's name with a directory so it cannot be opened.
		if err := os.Mkdir(filepath.Join(dir, "wallet.trace.jsonl"), 0o700); err != nil {
			t.Fatal(err)
		}
		if c := newTraceClient(t, Options{Storage: db}); c.traceFile != nil {
			t.Fatal("an unopenable default path should leave the trace file off")
		}
	})
}

func TestTraceFilePath(t *testing.T) {
	for db, want := range map[string]string{
		"/data/wallet.db":   "/data/wallet.trace.jsonl",
		"walletconnect.db":  "walletconnect.trace.jsonl",
		"/data/state":       "/data/state.trace.jsonl",
		"/data/app.v2.bolt": "/data/app.v2.trace.jsonl",
	} {
		if got := traceFilePath(db); got != want {
			t.Errorf("traceFilePath(%q) = %q, want %q", db, got, want)
		}
	}
}
