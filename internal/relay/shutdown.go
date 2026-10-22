package relay

import (
	"context"
	"encoding/json"
	"time"

	"github.com/coder/websocket"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

// drainTimeout bounds how long Close waits to flush acks and queued messages.
const drainTimeout = 3 * time.Second

// drain runs when the client closes while connected. It writes the acks the
// engine has queued, sends the queued fire-and-forget calls (such as answers
// to peer requests), waits for the relay to accept them, and then closes the
// socket with a normal close frame. Without it, a message processed just
// before Close is never acked, and the relay holds the peer's publish until
// its ~5 s redelivery.
func (r *runner) drain() {
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()

	r.c.cfg.Tracer.Event("relay closing", "socket", r.gen, "acks", len(r.c.acks), "queued", len(r.c.reqs)+len(r.c.prio)+len(r.unsent))
	for {
		select {
		case a := <-r.c.acks:
			r.writeAck(ctx, a)
			continue
		default:
		}
		break
	}

	queued := r.unsent
	r.unsent = nil
	for more := true; more; {
		select {
		case cl := <-r.c.prio:
			queued = append(queued, cl)
		case cl := <-r.c.reqs:
			queued = append(queued, cl)
		default:
			more = false
		}
	}
	for _, cl := range queued {
		if cl.reply != nil {
			r.finish(cl, nil, types.ErrClosed)
			continue
		}
		if !r.send(ctx, cl) {
			break
		}
	}

	for r.awaitingFireAndForget() {
		select {
		case f := <-r.frames:
			if f.err != nil {
				return
			}
			r.handleResponseFrame(f.data)
		case <-ctx.Done():
			r.c.log.Debug("relay drain timed out", "pending", len(r.pending))
			r.closeGracefully()
			return
		}
	}
	r.closeGracefully()
}

func (r *runner) awaitingFireAndForget() bool {
	for _, cl := range r.pending {
		if cl.reply == nil {
			return true
		}
	}
	return false
}

func (r *runner) closeGracefully() {
	if r.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = r.conn.Close(websocket.StatusNormalClosure, "client closed")
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	r.closeConn()
}

// writeAck acks a delivery on the socket it arrived on and ends its trace. A
// delivery from a replaced socket is not acked: the relay redelivers it.
func (r *runner) writeAck(ctx context.Context, a ack) {
	if a.gen != r.gen {
		a.span.End(nil, "acked", false, "reason", "socket replaced; the relay redelivers it")
		return
	}
	if err := r.write(ctx, ackFrame(a.id)); err != nil {
		a.span.End(err, "acked", false)
		return
	}
	a.span.End(nil, "acked", true)
}

func ackFrame(id []byte) []byte {
	return mustJSON(map[string]any{"id": json.RawMessage(id), "jsonrpc": jsonrpc.Version, "result": true})
}
