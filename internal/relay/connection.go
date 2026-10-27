package relay

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/WalletConnect/walletconnect-go/internal/relayauth"
)

type frame struct {
	gen  uint64
	data []byte
	err  error
}

func (r *runner) relayURL() (string, error) {
	base, _, _ := strings.Cut(r.c.cfg.URL, "?")
	jwt, err := relayauth.SignJWT(r.c.cfg.Key, r.c.jwtSub, base, time.Now().Unix(), jwtTTL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(r.c.cfg.URL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("auth", jwt)
	q.Set("projectId", r.c.cfg.ProjectID)
	q.Set("ua", r.c.cfg.UserAgent)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (r *runner) connecting(ctx context.Context) state {
	u, err := r.relayURL()
	if err != nil {
		r.c.log.Error("build relay url", "err", err)
		return stBackoff
	}
	span, sctx := r.c.cfg.Tracer.Child(ctx, "relay.connect", "attempt", r.attempt, "socket", r.gen+1)
	dialStart := time.Now()
	dialCtx, cancel := context.WithTimeout(ctx, r.c.cfg.DialTimeout)
	conn, resp, err := websocket.Dial(dialCtx, u, nil)
	cancel()
	if err != nil {
		span.End(err)
		r.c.cfg.Tracer.RecordConnect(sctx, time.Since(dialStart), false)
		if ctx.Err() != nil {
			return stStopped
		}
		// The relay rejects a bad JWT with 401 and an unknown project ID
		// with 403. Neither fixes itself, so stop instead of retrying.
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			r.c.log.Error("relay rejected the connection; check the project ID", "status", resp.StatusCode)
			return stPoisoned
		}
		r.c.log.Warn("relay dial failed", "err", err, "attempt", r.attempt)
		r.notify(false, err)
		return stBackoff
	}
	span.End(nil)
	r.c.cfg.Tracer.RecordConnect(sctx, time.Since(dialStart), true)
	r.c.cfg.Tracer.Connected(sctx, 1)
	conn.SetReadLimit(maxMessageSize)

	r.gen++
	// The read context is separate from ctx: coder/websocket closes the
	// socket when a read's context ends, and Close still needs the socket
	// to flush acks (see drain).
	connCtx, cancelConn := context.WithCancel(context.Background())
	r.conn, r.cancelConn = conn, cancelConn
	r.frames = make(chan frame, 64)
	r.pings = make(chan pingResult, 1)
	r.probing = false
	r.lastRead = time.Now()
	r.subs = map[string]bool{}
	go readLoop(connCtx, conn, r.gen, r.frames)
	r.attempt = 0
	r.c.log.Debug("relay connected", "gen", r.gen)

	topics, err := r.c.cfg.Topics(ctx)
	if err != nil {
		r.c.log.Error("load topics for resubscribe", "err", err)
	}
	if len(topics) > 0 {
		for _, t := range topics {
			r.subs[t] = true
		}
		// One irn_batchSubscribe per batchSubscribeLimit topics; the relay rejects larger batches.
		r.unsent = append(batchSubscribeCalls(context.Background(), topics), r.unsent...)
	}
	r.c.cfg.Tracer.Event("relay connected", "socket", r.gen, "resubscribe", len(topics))
	r.notify(true, nil)
	return stConnected
}

// connected serves the socket until it fails or ctx ends. ctx only signals
// stopping: writes use their own context, so a cancel never cuts off an ack
// or a send halfway, and stopping goes through drain.
func (r *runner) connected(ctx context.Context) state {
	io := context.Background()
	for len(r.unsent) > 0 {
		cl := r.unsent[0]
		r.unsent = r.unsent[1:]
		if !r.send(io, cl) {
			return r.reconnect(nil, "send_failed")
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	pingTicker := time.NewTicker(r.c.cfg.PingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case cl := <-r.c.prio:
			if !r.send(io, cl) {
				return r.reconnect(nil, "send_failed")
			}
			continue
		default:
		}
		select {
		case cl := <-r.c.prio:
			if !r.send(io, cl) {
				return r.reconnect(nil, "send_failed")
			}
		case cl := <-r.c.reqs:
			if !r.send(io, cl) {
				return r.reconnect(nil, "send_failed")
			}
		case f := <-r.frames:
			if f.gen != r.gen {
				continue
			}
			r.lastRead = time.Now()
			if f.err != nil {
				if websocket.CloseStatus(f.err) == closeInvalidAuth {
					r.c.log.Error("relay closed connection: invalid auth")
					r.closeConn()
					return stPoisoned
				}
				if ctx.Err() != nil {
					return stStopped
				}
				r.c.log.Warn("relay connection lost", "err", f.err)
				r.c.cfg.Tracer.Event("relay connection lost", "socket", r.gen, "err", f.err)
				return r.reconnect(f.err, "connection_lost")
			}
			r.handleFrame(io, f.data)
		case a := <-r.c.acks:
			r.writeAck(io, a)
		case <-ticker.C:
			if r.checkTimeouts() && !r.probing {
				r.probing = true
				go r.ping(r.conn, r.gen, r.pings)
			}
		case <-pingTicker.C:
			if !r.probing {
				r.probing = true
				go r.ping(r.conn, r.gen, r.pings)
			}
		case p := <-r.pings:
			if p.gen != r.gen {
				continue
			}
			r.probing = false
			if p.err != nil {
				r.c.cfg.Tracer.Event("relay ping failed, reconnecting", "socket", r.gen, "err", p.err)
				return r.reconnect(errors.New("relay ping failed"), "ping_failed")
			}
			r.lastRead = time.Now()
		case <-r.c.nudge:
		case <-r.c.drop:
			r.c.log.Debug("reconnect requested")
			r.c.cfg.Tracer.Event("relay reconnect requested", "socket", r.gen)
			r.reconnect(nil, "requested")
			return stConnecting
		case <-ctx.Done():
			r.drain()
			return stStopped
		}
	}
}

func readLoop(ctx context.Context, conn *websocket.Conn, gen uint64, out chan<- frame) {
	for {
		_, data, err := conn.Read(ctx)
		select {
		case out <- frame{gen: gen, data: data, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

type pingResult struct {
	gen uint64
	err error
}

// ping checks the socket answers; the result goes back to the runner.
func (r *runner) ping(conn *websocket.Conn, gen uint64, out chan<- pingResult) {
	ctx, cancel := context.WithTimeout(context.Background(), r.c.cfg.RequestTimeout)
	defer cancel()
	err := conn.Ping(ctx)
	select {
	case out <- pingResult{gen: gen, err: err}:
	default:
	}
}

func (r *runner) write(ctx context.Context, b []byte) error {
	wctx, cancel := context.WithTimeout(ctx, r.c.cfg.RequestTimeout)
	defer cancel()
	err := r.conn.Write(wctx, websocket.MessageText, b)
	if err != nil && ctx.Err() == nil {
		r.c.log.Warn("relay write failed", "err", err)
	}
	return err
}

func (r *runner) closeConn() {
	if r.conn == nil {
		return
	}
	r.cancelConn()
	r.conn.CloseNow()
	r.conn = nil
	r.c.cfg.Tracer.Connected(context.Background(), -1)
}
