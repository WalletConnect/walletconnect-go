package walletconnect

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/engine"
	"github.com/WalletConnect/walletconnect-go/internal/pulse"
	"github.com/WalletConnect/walletconnect-go/internal/queue"
	"github.com/WalletConnect/walletconnect-go/internal/relay"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/trace"
	"github.com/WalletConnect/walletconnect-go/storage"
	"github.com/WalletConnect/walletconnect-go/tracefile"
	"go.opentelemetry.io/otel/metric"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Version is the SDK version reported to the relay.
const Version = "1.0.0"

// DefaultRelayURL is the production relay.
const DefaultRelayURL = "wss://relay.walletconnect.org"

// Options configure a Client. Only ProjectID is required.
type Options struct {
	// ProjectID from the WalletConnect dashboard. Required.
	ProjectID string
	// Metadata describes this app to its peers.
	Metadata Metadata
	// RelayURL defaults to DefaultRelayURL.
	RelayURL string
	// Storage defaults to storage.NewMemory(), which loses state on exit.
	Storage storage.KV
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// EventBuffer is the capacity of the Events channel, default 64.
	EventBuffer int
	// Trace logs every action from start to finish at info level: each API
	// call and each incoming relay message gets a trace ID, and every relay
	// send, reply, delivery and ack is logged under it with elapsed times.
	Trace bool
	// TracerProvider and MeterProvider receive OpenTelemetry spans and
	// metrics for every action, relay call and incoming message; nil turns
	// each off. Pass otel.GetTracerProvider() and otel.GetMeterProvider() to
	// use the globals. They are independent of Trace: spans and metrics do
	// not need the log lines, and the log lines do not need them.
	TracerProvider oteltrace.TracerProvider
	MeterProvider  metric.MeterProvider
	// TraceFile is where every span is written, one per line in OTLP JSON,
	// rotated by size. With a bbolt Storage it defaults to a .trace.jsonl
	// file beside the database; with other storage it is off unless set.
	// The file needs no OpenTelemetry setup; see the tracefile package to
	// read or summarize it. An explicit path that cannot be opened fails New.
	TraceFile string
	// NoTraceFile turns the trace file off.
	NoTraceFile bool
	// TraceFileLimit is the most bytes the trace file and its two rotated
	// predecessors keep on disk; default 30 MB.
	TraceFileLimit int64
	// NoTelemetry stops reporting to WalletConnect's Pulse API, which the
	// JS SDK reports to as well. Each API call is reported as one event
	// when it ends: the action, its topic, the names of its steps, its
	// duration with the parts spent on the relay, waiting for a relay
	// connection and waiting for the peer and, if it failed, the error's
	// type. No params, payloads, keys, addresses or error messages are
	// sent. Events are sent in batches every 30 seconds and on Close, with Metadata.URL's origin as
	// the Origin header; Pulse accepts only the project's allowed domains,
	// and without a Metadata.URL nothing is reported. Test binaries never
	// report.
	NoTelemetry bool

	now      func() time.Time // test hook for the protocol clock
	pulseURL string           // test hook: report to this URL, even in tests
}

// Client is one WalletConnect client, acting as a dApp, a wallet or both.
// Its methods are safe for concurrent use. Close stops it.
type Client struct {
	tracer    *trace.Tracer
	traceFile *tracefile.Writer // nil when off
	pulse     *pulse.Reporter   // nil when off
	engine    *engine.Engine
	relay     *relay.Client
	// Stopped in this order by Close, so acks for processed messages and
	// queued answers reach the relay before the socket closes.
	stopEngine, stopRelay, stopDispatch context.CancelFunc
	events                              chan Event
	queue                               *queue.Queue[Event]
	wg                                  sync.WaitGroup
	closed                              sync.Once
}

// New loads or creates the client identity, starts the background
// goroutines and, if the storage holds any sessions or pairings, connects
// to the relay to resume them.
func New(ctx context.Context, opts Options) (*Client, error) {
	opts, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	st := store.New(opts.Storage)
	seed, err := loadSeed(ctx, st)
	if err != nil {
		return nil, fmt.Errorf("load client identity: %w", err)
	}
	topics := func(ctx context.Context) (topics []string, err error) {
		err = st.View(ctx, func(tx *store.Tx) error {
			topics, err = tx.Topics()
			return err
		})
		return topics, err
	}

	traceFile, err := openTraceFile(opts)
	if err != nil {
		return nil, err
	}
	var exporters []trace.Exporter
	if traceFile != nil {
		exporters = append(exporters, traceFile)
	}
	var reporter *pulse.Reporter
	if !opts.NoTelemetry && (opts.pulseURL != "" || !testing.Testing()) {
		reporter, err = pulse.New(pulse.Options{URL: opts.pulseURL, ProjectID: opts.ProjectID, Domain: opts.Metadata.URL,
			Version: Version, Logger: opts.Logger})
		if err != nil {
			opts.Logger.Info("telemetry is off: Metadata.URL is not a URL", "url", opts.Metadata.URL)
		} else {
			exporters = append(exporters, reporter)
		}
	}
	tracer := trace.New(trace.Options{Logger: opts.Logger, LogLines: opts.Trace, Exporter: trace.Exporters(exporters...),
		TracerProvider: opts.TracerProvider, MeterProvider: opts.MeterProvider, Version: Version})
	engineCtx, stopEngine := context.WithCancel(context.Background())
	relayCtx, stopRelay := context.WithCancel(context.Background())
	dispatchCtx, stopDispatch := context.WithCancel(context.Background())
	c := &Client{tracer: tracer, traceFile: traceFile, pulse: reporter, stopEngine: stopEngine, stopRelay: stopRelay, stopDispatch: stopDispatch,
		events: make(chan Event, opts.EventBuffer), queue: queue.New[Event]()}
	c.engine = engine.New(engine.Config{Store: st, Metadata: opts.Metadata, Logger: opts.Logger, Emit: c.queue.Push, Now: opts.now, Tracer: tracer})
	c.relay = relay.New(relay.Config{
		URL:       opts.RelayURL,
		ProjectID: opts.ProjectID,
		UserAgent: fmt.Sprintf("wc-2/go-%s/%s/server", Version, runtime.GOOS),
		Key:       ed25519.NewKeyFromSeed(seed),
		Topics:    topics,
		Deliver:   c.engine.Deliver,
		OnState: func(connected bool, err error) {
			c.queue.Push(EventRelayState{Connected: connected, Err: err})
		},
		Logger: opts.Logger,
		Tracer: tracer,
	})
	c.engine.SetRelay(c.relay)

	c.wg.Add(3)
	go func() { defer c.wg.Done(); c.relay.Run(relayCtx) }()
	go func() { defer c.wg.Done(); c.engine.Run(engineCtx) }()
	go func() { defer c.wg.Done(); c.dispatch(dispatchCtx) }()

	// Resume: connect now if there are sessions or pairings to listen on.
	if existing, err := topics(ctx); err == nil && len(existing) > 0 {
		c.relay.Connect()
	}
	return c, nil
}

// openTraceFile opens the trace file Options ask for: the given path, or
// one beside a file-backed storage. A default path that cannot be opened
// only logs a warning, so a read-only data directory does not stop the
// client.
func openTraceFile(o Options) (*tracefile.Writer, error) {
	if o.NoTraceFile {
		return nil, nil
	}
	path, explicit := o.TraceFile, o.TraceFile != ""
	if !explicit {
		p, ok := o.Storage.(interface{ Path() string })
		if !ok || p.Path() == "" {
			return nil, nil
		}
		path = traceFilePath(p.Path())
	}
	w, err := tracefile.Open(path, tracefile.Options{Limit: o.TraceFileLimit, Service: o.Metadata.Name, Version: Version})
	if err != nil {
		if explicit {
			return nil, fmt.Errorf("open trace file: %w", err)
		}
		o.Logger.Warn("trace file is off: cannot open it", "path", path, "err", err)
		return nil, nil
	}
	return w, nil
}

// traceFilePath puts the trace file beside the database, with its name:
// wallet.db becomes wallet.trace.jsonl.
func traceFilePath(db string) string {
	return strings.TrimSuffix(db, filepath.Ext(db)) + ".trace.jsonl"
}

func (o Options) withDefaults() (Options, error) {
	if o.ProjectID == "" {
		return o, fmt.Errorf("%w: ProjectID is required", ErrInvalidParams)
	}
	if o.RelayURL == "" {
		o.RelayURL = DefaultRelayURL
	}
	if o.Storage == nil {
		o.Storage = storage.NewMemory()
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.EventBuffer <= 0 {
		o.EventBuffer = 64
	}
	return o, nil
}

func loadSeed(ctx context.Context, st *store.Store) ([]byte, error) {
	var seed []byte
	err := st.Update(ctx, func(tx *store.Tx) error {
		s, err := tx.Seed()
		if err != nil {
			return err
		}
		if len(s) == ed25519.SeedSize {
			seed = s
			return nil
		}
		k, err := crypto.RandomKey()
		if err != nil {
			return err
		}
		seed = k[:]
		return tx.PutSeed(seed)
	})
	return seed, err
}

// Close stops the client and closes the Events channel. Before the socket
// closes it acks every message already processed and sends queued answers,
// waiting up to a few seconds. Storage is left as is.
func (c *Client) Close() error {
	c.closed.Do(func() {
		c.stopEngine()
		<-c.engine.Done()
		c.stopRelay()
		<-c.relay.Done()
		c.stopDispatch()
		c.wg.Wait()
		close(c.events)
		if c.pulse != nil {
			c.pulse.Close()
		}
		if c.traceFile != nil {
			c.traceFile.Close()
		}
	})
	return nil
}

// ClientID is the did:key identity this client authenticates to the relay with.
func (c *Client) ClientID() string { return c.relay.ClientID() }
