package relay

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/coder/websocket"

	"github.com/WalletConnect/walletconnect-go/internal/types"
)

type state int

const (
	stIdle state = iota
	stConnecting
	stConnected
	stBackoff
	stPoisoned
	stStopped
)

type runner struct {
	c       *Client
	pending map[int64]*call // sent on the current connection, awaiting a response
	unsent  []*call         // waiting for a connection
	attempt int

	conn       *websocket.Conn
	cancelConn context.CancelFunc
	gen        uint64
	frames     chan frame
	pings      chan pingResult
	probing    bool
	lastRead   time.Time       // latest frame or ping answer on this socket
	subs       map[string]bool // topics subscribed on this socket
}

func (r *runner) run(ctx context.Context) {
	st := stIdle
	for st != stStopped {
		switch st {
		case stIdle:
			st = r.idle(ctx)
		case stConnecting:
			st = r.connecting(ctx)
		case stConnected:
			st = r.connected(ctx)
		case stBackoff:
			st = r.backoff(ctx)
		case stPoisoned:
			st = r.poisoned(ctx)
		}
	}
	r.closeConn()
	r.failAll(types.ErrClosed)
}

func (r *runner) idle(ctx context.Context) state {
	select {
	case cl := <-r.c.prio:
		r.unsent = append(r.unsent, cl)
	case cl := <-r.c.reqs:
		r.unsent = append(r.unsent, cl)
	case <-r.c.nudge:
	case <-ctx.Done():
		return stStopped
	}
	return stConnecting
}

func (r *runner) backoff(ctx context.Context) state {
	b := r.c.cfg.Backoff
	d := b[min(r.attempt, len(b)-1)]
	d = time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
	r.attempt++
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case cl := <-r.c.prio:
			r.unsent = append(r.unsent, cl)
		case cl := <-r.c.reqs:
			r.unsent = append(r.unsent, cl)
		case <-r.c.nudge:
		case <-timer.C:
			return stConnecting
		case <-ctx.Done():
			return stStopped
		}
	}
}

func (r *runner) poisoned(ctx context.Context) state {
	r.failAll(types.ErrInvalidAuth)
	r.notify(false, types.ErrInvalidAuth)
	for {
		select {
		case cl := <-r.c.prio:
			r.finish(cl, nil, types.ErrInvalidAuth)
		case cl := <-r.c.reqs:
			r.finish(cl, nil, types.ErrInvalidAuth)
		case <-r.c.nudge:
		case <-ctx.Done():
			return stStopped
		}
	}
}

// reconnect drops the connection and queues every in-flight call for resend.
func (r *runner) reconnect(err error) state {
	r.closeConn()
	r.requeue()
	r.notify(false, err)
	return stBackoff
}

func (r *runner) notify(connected bool, err error) {
	if r.c.cfg.OnState != nil {
		r.c.cfg.OnState(connected, err)
	}
}
