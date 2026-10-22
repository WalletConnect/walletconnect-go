package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
)

type peer struct {
	*Client
	mu      sync.Mutex
	topics  []string
	in      chan Incoming
	autoAck bool
	states  chan bool
}

func newPeer(t *testing.T, ctx context.Context, url, projectID string) *peer {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	return newPeerWithKey(t, ctx, url, projectID, priv, nil)
}

// newPeerWithKey starts a peer with a fixed identity and initial topics, as a
// restarted client with stored sessions would.
func newPeerWithKey(t *testing.T, ctx context.Context, url, projectID string, priv ed25519.PrivateKey, topics []string) *peer {
	t.Helper()
	p := &peer{in: make(chan Incoming, 100), autoAck: true, states: make(chan bool, 100), topics: topics}
	p.Client = New(Config{
		URL: url, ProjectID: projectID, UserAgent: "wc-2/go-test/test/test", Key: priv,
		Topics: func(context.Context) ([]string, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			return append([]string(nil), p.topics...), nil
		},
		Deliver: func(in Incoming) {
			p.mu.Lock()
			auto := p.autoAck
			p.mu.Unlock()
			if auto {
				p.Ack(in)
			}
			p.in <- in
		},
		OnState: func(connected bool, _ error) { p.states <- connected },
		Logger:  testLogger(),
		Backoff: []time.Duration{100 * time.Millisecond},
	})
	go p.Run(ctx)
	return p
}

func (p *peer) setAutoAck(v bool) {
	p.mu.Lock()
	p.autoAck = v
	p.mu.Unlock()
}

func (p *peer) subscribe(t *testing.T, ctx context.Context, topic string) {
	t.Helper()
	p.mu.Lock()
	p.topics = append(p.topics, topic)
	p.mu.Unlock()
	if err := p.Subscribe(ctx, topic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
}

func randomTopic(t *testing.T) string {
	k, err := crypto.RandomKey()
	if err != nil {
		t.Fatal(err)
	}
	return crypto.Topic(k)
}

func randomMessage(t *testing.T) string {
	k, _ := crypto.RandomKey()
	msg, err := crypto.Encrypt(k, []byte(`{"test":true}`))
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func publish(t *testing.T, ctx context.Context, c *Client, topic, msg string) {
	t.Helper()
	raw, err := c.Request(ctx, "irn_publish", map[string]any{"topic": topic, "message": msg, "ttl": 300, "tag": 1108})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	var ok bool
	if json.Unmarshal(raw, &ok) != nil || !ok {
		t.Fatalf("publish result = %s", raw)
	}
}

// recvMessage waits for msg, skipping other deliveries.
func recvMessage(t *testing.T, p *peer, msg string) Incoming {
	t.Helper()
	timeout := time.After(20 * time.Second)
	for {
		select {
		case in := <-p.in:
			if in.Message == msg {
				return in
			}
		case <-timeout:
			t.Fatal("timed out waiting for message")
			return Incoming{}
		}
	}
}

// drainMessage collects every delivery of msg for d.
func drainMessage(p *peer, msg string, d time.Duration) []Incoming {
	var out []Incoming
	timeout := time.After(d)
	for {
		select {
		case in := <-p.in:
			if in.Message == msg {
				out = append(out, in)
			}
		case <-timeout:
			return out
		}
	}
}

func noMessage(t *testing.T, p *peer, msg string, d time.Duration) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case in := <-p.in:
			if in.Message == msg {
				t.Fatalf("unexpected redelivery of %q", msg)
			}
		case <-timeout:
			return
		}
	}
}

func waitConnected(t *testing.T, p *peer) {
	t.Helper()
	timeout := time.After(20 * time.Second)
	for {
		select {
		case up := <-p.states:
			if up {
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for reconnect")
		}
	}
}

func testLogger() *slog.Logger {
	if os.Getenv("RELAY_DEBUG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
