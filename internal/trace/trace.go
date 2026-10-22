// Package trace follows one action, such as a request or an incoming relay
// message, from start to finish. Every line of a span shares its trace ID
// and reports the time since the span started, so an action can be read in
// order across the API, the engine and the relay transport.
//
// A nil *Tracer or *Span is valid and does nothing, so tracing costs nothing
// when it is off.
package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"
)

type Tracer struct{ log *slog.Logger }

// New returns a tracer writing to log, or nil (tracing off) if on is false.
func New(log *slog.Logger, on bool) *Tracer {
	if !on {
		return nil
	}
	return &Tracer{log: log.With("component", "trace")}
}

type Span struct {
	t      *Tracer
	id     string
	action string
	start  time.Time
}

// Start opens a span and logs its first line.
func (t *Tracer) Start(action string, attrs ...any) *Span {
	if t == nil {
		return nil
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	s := &Span{t: t, id: hex.EncodeToString(b), action: action, start: time.Now()}
	s.emit("start", nil, attrs)
	return s
}

// Event logs a one-line span for something with no duration, such as the
// relay socket connecting.
func (t *Tracer) Event(action string, attrs ...any) {
	if s := t.Start(action, attrs...); s != nil {
		s.End(nil)
	}
}

// Step logs progress. Steps may follow End: a peer answer can arrive after
// the API call that sent the request has returned.
func (s *Span) Step(step string, attrs ...any) {
	if s != nil {
		s.emit(step, nil, attrs)
	}
}

// End logs the outcome: "done", or "failed" with err.
func (s *Span) End(err error, attrs ...any) {
	if s == nil {
		return
	}
	if err != nil {
		s.emit("failed", err, attrs)
		return
	}
	s.emit("done", nil, attrs)
}

// ID is the span's trace ID, or "" for a nil span.
func (s *Span) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

func (s *Span) emit(step string, err error, attrs []any) {
	args := append([]any{"trace", s.id, "action", s.action, "step", step,
		"elapsed", time.Since(s.start).Round(time.Microsecond)}, attrs...)
	if err != nil {
		args = append(args, "err", err)
	}
	s.t.log.Info("trace", args...)
}

type ctxKey struct{}

// With returns ctx carrying span.
func With(ctx context.Context, s *Span) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, s)
}

// From returns the span ctx carries, or nil.
func From(ctx context.Context) *Span {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(ctxKey{}).(*Span)
	return s
}
