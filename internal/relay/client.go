// Package relay is the WebSocket transport to the WalletConnect relay.
//
// One goroutine (Client.Run) owns the connection and is its only writer;
// callers talk to it through channels. It reconnects with backoff,
// re-subscribes every known topic after each connect, resends requests that
// were in flight, and acks incoming messages only once the engine has
// processed them.

package relay

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/coder/websocket"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/relayauth"
	"github.com/WalletConnect/walletconnect-go/internal/trace"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

const (
	closeInvalidAuth = websocket.StatusCode(3000)
	jwtTTL           = 86400
	maxMessageSize   = 16 << 20
	// A request fails with ErrTimeout after RequestTimeout × requestDeadlineFactor.
	requestDeadlineFactor = 3
)

// Incoming is an irn_subscription delivered to the engine, which hands it
// back to Ack once processed.
type Incoming struct {
	ID          json.RawMessage
	Gen         uint64
	Span        *trace.Span // the message's trace, ended when it is acked
	Received    time.Time   // when the frame was read; the incoming latency metric counts from here
	Topic       string
	Message     string
	Tag         int
	PublishedAt int64
}

// Elapsed is the time since the message was read, or 0 when Received is
// unset. It does not depend on tracing, so the latency metric works with a
// meter provider alone.
func (in Incoming) Elapsed() time.Duration {
	if in.Received.IsZero() {
		return 0
	}
	return time.Since(in.Received)
}

type Config struct {
	URL       string
	ProjectID string
	UserAgent string
	Key       ed25519.PrivateKey
	// Topics returns the topics to batch-subscribe after every connect.
	Topics func(ctx context.Context) ([]string, error)
	// Deliver receives incoming messages. It must not block.
	Deliver func(Incoming)
	// OnState is told about connection changes. It must not block.
	OnState func(connected bool, err error)
	Logger  *slog.Logger
	// Tracer follows every call and delivery; nil turns tracing off.
	Tracer *trace.Tracer

	RequestTimeout time.Duration // silence before probing the socket, default 10s
	DialTimeout    time.Duration // default 15s
	PingInterval   time.Duration // default 20s
	Backoff        []time.Duration
}

type Client struct {
	cfg      Config
	clientID string
	jwtSub   string
	log      *slog.Logger

	reqs  chan *call
	prio  chan *call
	acks  chan ack
	nudge chan struct{}
	drop  chan struct{}
	done  chan struct{}
}

func New(cfg Config) *Client {
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 10 * time.Second
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 15 * time.Second
	}
	if cfg.PingInterval == 0 {
		cfg.PingInterval = 20 * time.Second
	}
	if len(cfg.Backoff) == 0 {
		cfg.Backoff = []time.Duration{time.Second, time.Second, 2 * time.Second, 5 * time.Second}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	sub, _ := crypto.RandomKey()
	return &Client{
		cfg:      cfg,
		clientID: relayauth.ClientID(cfg.Key.Public().(ed25519.PublicKey)),
		jwtSub:   hex.EncodeToString(sub[:]),
		log:      cfg.Logger.With("component", "relay"),
		reqs:     make(chan *call, 1024),
		prio:     make(chan *call, 1024),
		acks:     make(chan ack, 1024),
		nudge:    make(chan struct{}, 1),
		drop:     make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
}

func (c *Client) ClientID() string { return c.clientID }

// SubscriptionID is the ID the relay assigns to this client's subscription.
func (c *Client) SubscriptionID(topic string) string {
	return crypto.HashMessage(topic + c.clientID)
}

// Request sends a relay request and waits for its result.
func (c *Client) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.do(ctx, &call{method: method, params: params})
}

// RequestSubscribing sends a relay request that subscribes this client to
// topic on the relay's side, and waits for its result. The topic then counts
// as subscribed on the current socket, so Unsubscribe drops it.
func (c *Client) RequestSubscribing(ctx context.Context, method string, params any, topic string) (json.RawMessage, error) {
	return c.do(ctx, &call{method: method, params: params, subscribes: topic})
}

// Enqueue sends a relay request without waiting. Failures are logged. ctx
// only carries the trace: cancelling it does not stop the send.
func (c *Client) Enqueue(ctx context.Context, method string, params any, priority bool) {
	c.enqueue(ctx, &call{method: method, params: params}, priority)
}

// Subscribe subscribes to topic and waits for the relay. A topic already
// subscribed on the current socket is not subscribed again.
func (c *Client) Subscribe(ctx context.Context, topic string) error {
	_, err := c.do(ctx, c.subscribeCall(topic))
	return err
}

// SubscribeAsync subscribes to topic ahead of other queued requests, without
// waiting.
func (c *Client) SubscribeAsync(ctx context.Context, topic string) {
	c.enqueue(ctx, c.subscribeCall(topic), true)
}

// Unsubscribe drops the subscription to topic without waiting. A topic not
// subscribed on the current socket is skipped.
func (c *Client) Unsubscribe(ctx context.Context, topic string) {
	c.enqueue(ctx, &call{op: opUnsubscribe, method: "irn_unsubscribe", topic: topic,
		params: map[string]any{"topic": topic, "id": c.SubscriptionID(topic)}}, false)
}

func (c *Client) subscribeCall(topic string) *call {
	return &call{op: opSubscribe, method: "irn_subscribe", topic: topic, params: map[string]any{"topic": topic}}
}

func (c *Client) do(ctx context.Context, cl *call) (json.RawMessage, error) {
	cl.ctx, cl.reply = ctx, make(chan result, 1)
	if err := c.submit(ctx, c.reqs, cl); err != nil {
		return nil, err
	}
	select {
	case r := <-cl.reply:
		return r.raw, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, types.ErrClosed
	}
}

func (c *Client) enqueue(ctx context.Context, cl *call, priority bool) {
	ch := c.reqs
	if priority {
		ch = c.prio
	}
	cl.ctx = context.WithoutCancel(ctx)
	_ = c.submit(context.Background(), ch, cl)
}

func (c *Client) submit(ctx context.Context, ch chan *call, cl *call) error {
	select {
	case ch <- cl:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return types.ErrClosed
	}
}

// Ack confirms an incoming message on the connection it arrived on.
func (c *Client) Ack(in Incoming) {
	select {
	case c.acks <- ack{gen: in.Gen, id: in.ID, span: in.Span}:
	case <-c.done:
		in.Span.End(types.ErrClosed, "acked", false)
	}
}

// Connect asks the transport to connect even if no request is queued.
func (c *Client) Connect() {
	select {
	case c.nudge <- struct{}{}:
	default:
	}
}

// Reconnect drops the current connection and connects again, resending
// in-flight requests and resubscribing. Use it after a network change.
func (c *Client) Reconnect() {
	select {
	case c.drop <- struct{}{}:
	default:
	}
}

// Run owns the connection until ctx is cancelled. On cancel it flushes queued
// acks and fire-and-forget calls before closing the socket (see drain).
func (c *Client) Run(ctx context.Context) {
	defer close(c.done)
	r := &runner{c: c, pending: map[int64]*call{}}
	r.run(ctx)
}

// Done is closed when Run returns.
func (c *Client) Done() <-chan struct{} { return c.done }
