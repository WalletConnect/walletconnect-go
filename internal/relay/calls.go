package relay

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/trace"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

type result struct {
	raw json.RawMessage
	err error
}

type callOp int

const (
	opRequest callOp = iota
	opSubscribe
	opUnsubscribe
	opBatchSubscribe
)

type call struct {
	ctx    context.Context
	op     callOp
	method string
	params any
	topic  string      // opSubscribe, opUnsubscribe
	topics []string    // opBatchSubscribe
	reply  chan result // nil for fire-and-forget

	id        int64
	frame     []byte
	firstSent time.Time // first write, on any connection
	sentAt    time.Time // latest write
}

type ack struct {
	gen  uint64
	id   json.RawMessage
	span *trace.Span
}

// send writes a call. It returns false if the connection is broken, in which
// case the call has been put back for resending.
func (r *runner) send(ctx context.Context, cl *call) bool {
	if err := cl.ctx.Err(); err != nil {
		r.finish(cl, nil, err)
		return true
	}
	switch cl.op {
	case opSubscribe:
		if !r.claimSubscribe(cl) {
			return true
		}
	case opUnsubscribe:
		if !r.claimUnsubscribe(cl) {
			return true
		}
	}
	if cl.id == 0 {
		cl.id = jsonrpc.RelayID()
		b, err := json.Marshal(jsonrpc.Request{ID: cl.id, JSONRPC: jsonrpc.Version, Method: cl.method, Params: cl.params})
		if err != nil {
			r.finish(cl, nil, err)
			return true
		}
		cl.frame = b
	}
	if err := r.write(ctx, cl.frame); err != nil {
		r.unsent = append([]*call{cl}, r.unsent...)
		return false
	}
	cl.sentAt = time.Now()
	if cl.firstSent.IsZero() {
		cl.firstSent = cl.sentAt
	}
	r.pending[cl.id] = cl
	trace.From(cl.ctx).Step("relay send", append([]any{"method", cl.method, "relayId", cl.id, "socket", r.gen},
		describeParams(cl.params)...)...)
	return true
}

// describeParams picks the topic and tag out of a call's params for logs.
func describeParams(params any) []any {
	m, ok := params.(map[string]any)
	if !ok {
		return nil
	}
	var out []any
	for _, k := range []string{"topic", "pairingTopic", "sessionTopic", "tag"} {
		if v, ok := m[k]; ok {
			out = append(out, k, v)
		}
	}
	if ts, ok := m["topics"].([]string); ok {
		out = append(out, "topics", len(ts))
	}
	return out
}

// checkTimeouts fails calls past their overall deadline and reports whether
// the socket needs probing: a call is overdue and nothing has arrived since
// it was sent. A slow answer on a socket that is otherwise talking is not a
// stall; the relay holds a publish response until the receiver acks, which
// can take about 5 s.
func (r *runner) checkTimeouts() (probe bool) {
	now := time.Now()
	overall := r.c.cfg.RequestTimeout * requestDeadlineFactor
	for id, cl := range r.pending {
		switch {
		case now.Sub(cl.firstSent) >= overall:
			delete(r.pending, id)
			trace.From(cl.ctx).Step("relay timeout", "method", cl.method, "relayId", cl.id)
			r.finish(cl, nil, types.ErrTimeout)
		case now.Sub(cl.sentAt) >= r.c.cfg.RequestTimeout && r.lastRead.Before(cl.sentAt):
			probe = true
		}
	}
	return probe
}

// requeue moves in-flight calls back in front of the unsent ones.
func (r *runner) requeue() {
	ids := make([]int64, 0, len(r.pending))
	for id := range r.pending {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	resend := make([]*call, 0, len(ids)+len(r.unsent))
	for _, id := range ids {
		resend = append(resend, r.pending[id])
	}
	r.pending = map[int64]*call{}
	r.unsent = append(resend, r.unsent...)
}

func (r *runner) finish(cl *call, raw json.RawMessage, err error) {
	if cl.reply != nil {
		cl.reply <- result{raw: raw, err: err}
		return
	}
	switch {
	case err == nil:
	case errors.Is(err, types.ErrClosed):
		r.c.log.Debug("relay request dropped at shutdown", "method", cl.method)
	default:
		r.c.log.Warn("relay request failed", "method", cl.method, "err", err)
	}
}

func (r *runner) failAll(err error) {
	for id, cl := range r.pending {
		delete(r.pending, id)
		r.finish(cl, nil, err)
	}
	for _, cl := range r.unsent {
		r.finish(cl, nil, err)
	}
	r.unsent = nil
}
