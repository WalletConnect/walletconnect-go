// Package engine implements the Sign v2 session protocol on top of the store
// and the relay transport.
//
// Incoming messages are processed one at a time on the engine goroutine (Run).
// Public methods run on the caller's goroutine. Both go through the store, which
// is the only durable state.

package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/queue"
	"github.com/WalletConnect/walletconnect-go/internal/relay"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/trace"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

// Relay is the subset of the relay transport the engine uses.
type Relay interface {
	Request(ctx context.Context, method string, params any) (json.RawMessage, error)
	RequestSubscribing(ctx context.Context, method string, params any, topic string) (json.RawMessage, error)
	Enqueue(ctx context.Context, method string, params any, priority bool)
	Ack(in relay.Incoming)
	Subscribe(ctx context.Context, topic string) error
	SubscribeAsync(ctx context.Context, topic string)
	Unsubscribe(ctx context.Context, topic string)
}

type Config struct {
	Store    *store.Store
	Relay    Relay
	Metadata types.Metadata
	Logger   *slog.Logger
	// Emit receives events. It must not block.
	Emit func(types.Event)
	Now  func() time.Time
	// Tracer follows actions and incoming messages; nil turns tracing off.
	Tracer *trace.Tracer
}

type Engine struct {
	store *store.Store
	relay Relay
	meta  types.Metadata
	log   *slog.Logger
	emit  func(types.Event)
	clock func() time.Time
	tr    *trace.Tracer

	incoming *queue.Queue[relay.Incoming]
	rearm    chan struct{}
	done     chan struct{}

	wmu     sync.Mutex
	waiters map[int64]*waiter

	// Last processed update and event IDs, for discarding out-of-order ones.
	omu        sync.Mutex
	lastUpdate map[string]int64
	lastEvent  map[string]int64
}

func New(cfg Config) *Engine {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Engine{
		store:      cfg.Store,
		relay:      cfg.Relay,
		meta:       cfg.Metadata.Normalized(),
		log:        cfg.Logger.With("component", "engine"),
		emit:       cfg.Emit,
		clock:      cfg.Now,
		tr:         cfg.Tracer,
		incoming:   queue.New[relay.Incoming](),
		rearm:      make(chan struct{}, 1),
		done:       make(chan struct{}),
		waiters:    map[int64]*waiter{},
		lastUpdate: map[string]int64{},
		lastEvent:  map[string]int64{},
	}
}

// SetRelay wires the transport after construction, breaking the init cycle.
func (e *Engine) SetRelay(r Relay) { e.relay = r }

// Deliver queues an incoming relay message. It never blocks.
func (e *Engine) Deliver(in relay.Incoming) { e.incoming.Push(in) }

func (e *Engine) Done() <-chan struct{} { return e.done }

func (e *Engine) now() int64 { return e.clock().Unix() }

// Run processes incoming messages and expiry until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	defer close(e.done)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-e.incoming.Signal():
			for _, in := range e.incoming.PopAll() {
				if e.handle(trace.With(ctx, in.Span), in) {
					in.Span.Step("ack queued")
					e.relay.Ack(in)
				}
			}
		case <-timer.C:
			if err := e.Sweep(ctx); err != nil && ctx.Err() == nil {
				e.log.Error("expiry sweep", "err", err)
			}
			timer.Reset(e.nextSweep(ctx))
		case <-e.rearm:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(e.nextSweep(ctx))
		case <-ctx.Done():
			e.failAllWaiters(types.ErrClosed)
			return
		}
	}
}

func (e *Engine) requestRearm() {
	select {
	case e.rearm <- struct{}{}:
	default:
	}
}

func (e *Engine) nextSweep(ctx context.Context) time.Duration {
	const maxWait = time.Hour
	var next int64
	if err := e.store.View(ctx, func(tx *store.Tx) (err error) {
		next, err = tx.NextExpiry()
		return err
	}); err != nil || next == 0 {
		return maxWait
	}
	d := time.Unix(next, 0).Sub(e.clock()) + 100*time.Millisecond
	return min(max(d, 0), maxWait)
}
