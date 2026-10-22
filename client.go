// Package walletconnect is a Go SDK for the WalletConnect Sign v2 protocol.
// One Client can act as a dApp, proposing sessions and sending requests, and
// as a wallet, approving sessions and answering requests.
//
// All state lives in the Storage passed in Options, so a client restarted on
// the same storage resumes its sessions.

package walletconnect

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/engine"
	"github.com/WalletConnect/walletconnect-go/internal/queue"
	"github.com/WalletConnect/walletconnect-go/internal/relay"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/trace"
	"github.com/WalletConnect/walletconnect-go/storage"
)

// Version is the SDK version reported to the relay.
const Version = "0.1.0"

// DefaultRelayURL is the production relay.
const DefaultRelayURL = "wss://relay.walletconnect.org"

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

	now func() time.Time // test hook for the protocol clock
}

type Client struct {
	tracer *trace.Tracer
	engine *engine.Engine
	relay  *relay.Client
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

	tracer := trace.New(opts.Logger, opts.Trace)
	engineCtx, stopEngine := context.WithCancel(context.Background())
	relayCtx, stopRelay := context.WithCancel(context.Background())
	dispatchCtx, stopDispatch := context.WithCancel(context.Background())
	c := &Client{tracer: tracer, stopEngine: stopEngine, stopRelay: stopRelay, stopDispatch: stopDispatch,
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
	})
	return nil
}

// ClientID is the did:key identity this client authenticates to the relay with.
func (c *Client) ClientID() string { return c.relay.ClientID() }
