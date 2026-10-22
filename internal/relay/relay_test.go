package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/testenv"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

func TestLivePublishSubscribe(t *testing.T) {
	url, project := testenv.Relay(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	a, b := newPeer(t, ctx, url, project), newPeer(t, ctx, url, project)

	topic, msg := randomTopic(t), randomMessage(t)
	b.subscribe(t, ctx, topic)
	publish(t, ctx, a.Client, topic, msg)
	in := recvMessage(t, b, msg)
	if in.Topic != topic || in.Tag != 1108 {
		t.Fatalf("got %+v", in)
	}
}

func TestLiveMailboxAndAck(t *testing.T) {
	url, project := testenv.Relay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	a, b := newPeer(t, ctx, url, project), newPeer(t, ctx, url, project)

	topic, msg := randomTopic(t), randomMessage(t)
	publish(t, ctx, a.Client, topic, msg)

	// Published before the subscription: the mailbox delivers it.
	b.setAutoAck(false)
	b.subscribe(t, ctx, topic)
	if got := drainMessage(b, msg, 3*time.Second); len(got) == 0 {
		t.Fatal("mailbox message not delivered")
	}

	// Unacked: delivered again on the new connection.
	b.Reconnect()
	waitConnected(t, b)
	got := drainMessage(b, msg, 3*time.Second)
	if len(got) == 0 {
		t.Fatal("unacked message not redelivered after reconnect")
	}

	// Acked on the live connection: not delivered again.
	latest := got[len(got)-1]
	b.Ack(latest)
	time.Sleep(time.Second)
	b.Reconnect()
	waitConnected(t, b)
	noMessage(t, b, msg, 5*time.Second)
}

func TestLiveReconnectResubscribesAndResends(t *testing.T) {
	url, project := testenv.Relay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	a, b := newPeer(t, ctx, url, project), newPeer(t, ctx, url, project)
	topic := randomTopic(t)
	b.subscribe(t, ctx, topic)

	b.Reconnect()
	waitConnected(t, b)
	msg := randomMessage(t)
	publish(t, ctx, a.Client, topic, msg)
	recvMessage(t, b, msg)

	// A publish queued while reconnecting is sent once the socket is back.
	a.Reconnect()
	msg2 := randomMessage(t)
	publish(t, ctx, a.Client, topic, msg2)
	recvMessage(t, b, msg2)
}

func TestLiveUnknownProjectIsFatal(t *testing.T) {
	url, _ := testenv.Relay(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p := newPeer(t, ctx, url, "00000000000000000000000000000000")
	err := p.Subscribe(ctx, randomTopic(t))
	if !errors.Is(err, types.ErrInvalidAuth) {
		t.Fatalf("err = %v, want ErrInvalidAuth", err)
	}
	if err := p.Subscribe(ctx, randomTopic(t)); !errors.Is(err, types.ErrInvalidAuth) {
		t.Fatalf("second request err = %v, want ErrInvalidAuth", err)
	}
}

// The relay does not answer requests it rejects, so they time out. The
// client must stay usable afterwards.
func TestLiveUnansweredRequestTimesOut(t *testing.T) {
	url, project := testenv.Relay(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p := newPeer(t, ctx, url, project)
	p.cfg.RequestTimeout = time.Second

	if _, err := p.Request(ctx, "irn_bogus", map[string]any{}); !errors.Is(err, types.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	// The socket answered pings throughout, so it was never replaced.
	for {
		select {
		case up := <-p.states:
			if !up {
				t.Fatal("client reconnected over an unanswered request on a healthy socket")
			}
			continue
		default:
		}
		break
	}
	q := newPeer(t, ctx, url, project)
	topic, msg := randomTopic(t), randomMessage(t)
	q.subscribe(t, ctx, topic)
	publish(t, ctx, p.Client, topic, msg)
	recvMessage(t, q, msg)
}

func TestLiveClose(t *testing.T) {
	url, project := testenv.Relay(t)
	ctx, cancel := context.WithCancel(context.Background())
	p := newPeer(t, ctx, url, project)
	p.subscribe(t, ctx, randomTopic(t))
	cancel()
	<-p.Done()
	if err := p.Subscribe(context.Background(), randomTopic(t)); !errors.Is(err, types.ErrClosed) {
		t.Fatalf("after close err = %v", err)
	}
}

// A topic both in the store (batch-subscribed on connect) and subscribed
// explicitly by the request that opened the socket is subscribed once, so a
// mailbox message is delivered once.
func TestLiveFirstSubscribeDeliversOnce(t *testing.T) {
	url, project := testenv.Relay(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	a := newPeer(t, ctx, url, project)
	topic, msg := randomTopic(t), randomMessage(t)
	publish(t, ctx, a.Client, topic, msg)

	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	b := newPeerWithKey(t, ctx, url, project, priv, []string{topic})
	if err := b.Subscribe(ctx, topic); err != nil {
		t.Fatal(err)
	}
	if got := drainMessage(b, msg, 4*time.Second); len(got) != 1 {
		t.Fatalf("mailbox message delivered %d times, want 1", len(got))
	}
}

// An ack given just before the client stops is still written: the peer's
// publish returns promptly instead of waiting for the relay's ~5 s
// redelivery, and a restarted client with the same identity does not get the
// message again.
func TestLiveAckFlushedOnClose(t *testing.T) {
	url, project := testenv.Relay(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	a := newPeer(t, ctx, url, project)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	topic, msg := randomTopic(t), randomMessage(t)

	bCtx, stopB := context.WithCancel(ctx)
	b := newPeerWithKey(t, bCtx, url, project, priv, nil)
	b.setAutoAck(false)
	b.subscribe(t, ctx, topic)

	// The relay answers a publish once the receiver acks it.
	published := make(chan time.Duration, 1)
	start := time.Now()
	go func() {
		_, _ = a.Request(ctx, "irn_publish", map[string]any{"topic": topic, "message": msg, "ttl": 300, "tag": 1108})
		published <- time.Since(start)
	}()
	in := recvMessage(t, b, msg)
	b.Ack(in)
	stopB()
	<-b.Done()
	if took := <-published; took > 3*time.Second {
		t.Fatalf("publish took %v: the ack did not reach the relay before close", took)
	}

	b2 := newPeerWithKey(t, ctx, url, project, priv, []string{topic})
	b2.Connect()
	noMessage(t, b2, msg, 7*time.Second)
}

// A fire-and-forget publish queued just before the client stops is sent.
func TestLiveQueuedPublishFlushedOnClose(t *testing.T) {
	url, project := testenv.Relay(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	b := newPeer(t, ctx, url, project)
	topic, msg := randomTopic(t), randomMessage(t)
	b.subscribe(t, ctx, topic)

	aCtx, stopA := context.WithCancel(ctx)
	a := newPeer(t, aCtx, url, project)
	if err := a.Subscribe(ctx, randomTopic(t)); err != nil { // open the socket
		t.Fatal(err)
	}
	a.Enqueue(ctx, "irn_publish", map[string]any{"topic": topic, "message": msg, "ttl": 300, "tag": 1108}, false)
	stopA()
	<-a.Done()
	recvMessage(t, b, msg)
}
