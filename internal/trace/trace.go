// Package trace follows one action, such as a request or an incoming relay
// message, from start to finish, and reports it two ways.
//
// Trace lines go to a logger: every line of a span shares its trace ID and
// reports the time since the span started, so an action can be read in order
// across the API, the engine and the relay transport.
//
// OpenTelemetry spans and metrics go to the providers the application
// installed. Each action, relay call and incoming message becomes a span
// carrying the identifiers needed to match it with relay-side logs (topic,
// relay JSON-RPC id, payload id, tag), and latencies are recorded as
// histograms with bounded attributes. Child spans measure one part of an
// action, such as the wait for the peer's answer, and exist only in
// OpenTelemetry: they add no log lines.
//
// A nil *Tracer or *Span is valid and does nothing. With no logger and no
// provider, Start returns nil spans without touching OpenTelemetry, so
// tracing costs nothing when it is off; with a provider that does not record,
// it costs one span start.
package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Scope is the OpenTelemetry instrumentation scope of every span and metric.
const Scope = "github.com/WalletConnect/walletconnect-go"

type Options struct {
	// Logger receives trace lines at info level when LogLines is set.
	Logger   *slog.Logger
	LogLines bool
	// TracerProvider and MeterProvider receive spans and metrics; nil turns
	// each off. Pass otel.GetTracerProvider() to use the globals.
	TracerProvider oteltrace.TracerProvider
	MeterProvider  metric.MeterProvider
	// Version is reported as the instrumentation version.
	Version string
}

type Tracer struct {
	log    *slog.Logger     // nil when trace lines are off
	tracer oteltrace.Tracer // nil when OpenTelemetry tracing is off
	m      instruments      // zero when OpenTelemetry metrics are off
}

// New returns a tracer for the given sinks. A tracer whose sinks are all
// off still works; its spans are nil.
func New(o Options) *Tracer {
	t := &Tracer{}
	if o.TracerProvider != nil {
		t.tracer = o.TracerProvider.Tracer(Scope, oteltrace.WithInstrumentationVersion(o.Version))
	}
	if o.MeterProvider != nil {
		t.m = newInstruments(o.MeterProvider.Meter(Scope, metric.WithInstrumentationVersion(o.Version)))
	}
	if o.LogLines && o.Logger != nil {
		t.log = o.Logger.With("component", "trace")
	}
	return t
}

type Span struct {
	t      *Tracer
	id     string
	action string
	start  time.Time
	sp     oteltrace.Span // nil when OpenTelemetry tracing is off
	quiet  bool           // a child span: OpenTelemetry only, no log lines
}

func (s *Span) recording() bool { return s.sp != nil && s.sp.IsRecording() }

// Start opens a span for an action and logs its first line. The returned
// context carries the span, so spans started from it become its children.
func (t *Tracer) Start(ctx context.Context, action string, attrs ...any) (*Span, context.Context) {
	return t.start(ctx, oteltrace.SpanKindInternal, action, false, attrs)
}

// StartIncoming opens a span for a message delivered by the relay.
func (t *Tracer) StartIncoming(ctx context.Context, action string, attrs ...any) (*Span, context.Context) {
	return t.start(ctx, oteltrace.SpanKindConsumer, action, false, attrs)
}

// Child opens an OpenTelemetry-only span under the span ctx carries, for a
// part of an action with its own duration. It writes no log lines. The span
// is nil when nothing records, so callers need no checks.
func (t *Tracer) Child(ctx context.Context, name string, attrs ...any) (*Span, context.Context) {
	return t.start(ctx, oteltrace.SpanKindClient, name, true, attrs)
}

// Event logs a one-line span for something with no duration, such as the
// relay socket connecting.
func (t *Tracer) Event(action string, attrs ...any) {
	if s, _ := t.Start(context.Background(), action, attrs...); s != nil {
		s.End(nil)
	}
}

func (t *Tracer) start(ctx context.Context, kind oteltrace.SpanKind, action string, quiet bool, attrs []any) (*Span, context.Context) {
	if t == nil {
		return nil, ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if t.tracer == nil && (quiet || t.log == nil) {
		return nil, ctx
	}
	parent := From(ctx)
	var sp oteltrace.Span
	if t.tracer != nil {
		ctx, sp = t.tracer.Start(ctx, spanName(action), spanKinds[kind])
		if !sp.IsRecording() {
			if quiet || t.log == nil {
				sp.End()
				return nil, ctx
			}
		} else {
			sp.SetAttributes(kv(attrs)...)
		}
	}
	s := &Span{t: t, action: action, start: time.Now(), sp: sp, quiet: quiet}
	switch {
	case sp != nil && sp.SpanContext().HasTraceID():
		s.id = sp.SpanContext().TraceID().String()[:8]
	case parent != nil:
		s.id = parent.id
	default:
		s.id = randomID()
	}
	ctx = context.WithValue(ctx, ctxKey{}, s)
	if !quiet {
		s.emit("start", nil, attrs)
	}
	return s, ctx
}

// Step logs progress and adds an event to the OpenTelemetry span. Steps may
// follow End on the log side: a peer answer can arrive after the API call
// that sent the request has returned.
func (s *Span) Step(step string, attrs ...any) {
	if s == nil {
		return
	}
	s.emit(step, nil, attrs)
	if s.recording() {
		s.sp.AddEvent(step, oteltrace.WithAttributes(kv(attrs)...))
	}
}

// End logs the outcome, "done" or "failed" with err, and ends the
// OpenTelemetry span with the matching status.
func (s *Span) End(err error, attrs ...any) {
	if s == nil {
		return
	}
	if err != nil {
		s.emit("failed", err, attrs)
	} else {
		s.emit("done", nil, attrs)
	}
	if s.recording() {
		if len(attrs) > 0 {
			s.sp.SetAttributes(kv(attrs)...)
		}
		if err != nil {
			s.sp.RecordError(err)
			s.sp.SetStatus(codes.Error, err.Error())
		} else {
			s.sp.SetStatus(codes.Ok, "")
		}
	}
	if s.sp != nil {
		s.sp.End()
	}
}

// Link records that other, usually the incoming message that answered this
// span's request, belongs to this span's story.
func (s *Span) Link(other *Span) {
	if s == nil || other == nil || other.sp == nil || !s.recording() {
		return
	}
	s.sp.AddLink(oteltrace.Link{SpanContext: other.sp.SpanContext()})
}

// ID is the span's trace ID, or "" for a nil span.
func (s *Span) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

// Elapsed is the time since the span started, or 0 for a nil span.
func (s *Span) Elapsed() time.Duration {
	if s == nil {
		return 0
	}
	return time.Since(s.start)
}

func (s *Span) emit(step string, err error, attrs []any) {
	if s.quiet || s.t.log == nil {
		return
	}
	args := append([]any{"trace", s.id, "action", s.action, "step", step,
		"elapsed", time.Since(s.start).Round(time.Microsecond)}, attrs...)
	if err != nil {
		args = append(args, "err", err)
	}
	s.t.log.Info("trace", args...)
}

// spanKinds are preallocated so a non-recording start allocates nothing for
// its options.
var spanKinds = map[oteltrace.SpanKind]oteltrace.SpanStartOption{
	oteltrace.SpanKindInternal: oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
	oteltrace.SpanKindClient:   oteltrace.WithSpanKind(oteltrace.SpanKindClient),
	oteltrace.SpanKindConsumer: oteltrace.WithSpanKind(oteltrace.SpanKindConsumer),
}

func randomID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type ctxKey struct{}

// With returns ctx carrying span, for both this package and OpenTelemetry.
func With(ctx context.Context, s *Span) context.Context {
	if s == nil {
		return ctx
	}
	if s.sp != nil {
		ctx = oteltrace.ContextWithSpan(ctx, s.sp)
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

// spanName maps a log action to an OpenTelemetry span name: API actions
// become walletconnect.<action>, relay events relay.<event>, and names that
// already contain a dot are used as given.
func spanName(action string) string {
	if strings.Contains(action, ".") {
		return action
	}
	words := strings.Fields(action)
	switch {
	case action == "incoming":
		return "relay.incoming"
	case len(words) > 1 && words[0] == "relay":
		return "relay." + strings.Join(words[1:], "_")
	default:
		return "walletconnect." + strings.Join(words, "_")
	}
}

// Attribute keys. Log lines keep the short keys documented in the README;
// OpenTelemetry attributes use these.
const (
	AttrTopic          = "wc.topic"
	AttrPairingTopic   = "wc.pairing_topic"
	AttrSessionTopic   = "wc.session_topic"
	AttrRPCID          = "wc.rpc.id"
	AttrProposalID     = "wc.proposal_id"
	AttrMethod         = "wc.method"
	AttrChainID        = "wc.chain_id"
	AttrKind           = "wc.kind"
	AttrOutcome        = "wc.outcome"
	AttrReason         = "wc.reason"
	AttrRelayID        = "relay.id"
	AttrRelayMethod    = "rpc.method"
	AttrRelayTag       = "relay.tag"
	AttrRelaySocket    = "relay.socket"
	AttrPublishedAt    = "relay.published_at"
	AttrSubscriptionID = "relay.subscription_id"
)

var attrKeys = map[string]string{
	"topic":          AttrTopic,
	"pairingTopic":   AttrPairingTopic,
	"sessionTopic":   AttrSessionTopic,
	"id":             AttrRPCID,
	"proposalId":     AttrProposalID,
	"chainId":        AttrChainID,
	"innerMethod":    AttrMethod,
	"kind":           AttrKind,
	"outcome":        AttrOutcome,
	"reason":         AttrReason,
	"relayId":        AttrRelayID,
	"tag":            AttrRelayTag,
	"socket":         AttrRelaySocket,
	"publishedAt":    AttrPublishedAt,
	"subscriptionId": AttrSubscriptionID,
	"topics":         "relay.topics",
	"resubscribe":    "relay.resubscribe",
	"attempt":        "relay.attempt",
	"took":           "relay.took",
	"relayError":     "relay.error",
	"acks":           "relay.acks",
	"queued":         "relay.queued",
	"via":            "wc.via",
	"acked":          "wc.acked",
	"after":          "wc.after",
	"code":           "wc.error_code",
	"message":        "wc.error_message",
	"event":          "wc.event",
	"namespaces":     "wc.namespaces",
	"err":            "error.message",
}

// kv converts the key/value pairs the call sites pass to log lines into
// OpenTelemetry attributes with stable names.
func kv(attrs []any) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(attrs)/2)
	for i := 0; i+1 < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		if !ok {
			continue
		}
		out = append(out, attr(key, attrs[i+1]))
	}
	return out
}

func attr(key string, v any) attribute.KeyValue {
	name := attrKeys[key]
	if name == "" {
		switch {
		case key == "method":
			if s, ok := v.(string); ok && (strings.HasPrefix(s, "irn_") || strings.HasPrefix(s, "wc_")) {
				name = AttrRelayMethod
			} else {
				name = AttrMethod
			}
		case strings.Contains(key, "."):
			name = key
		default:
			name = "wc." + key
		}
	}
	switch x := v.(type) {
	case string:
		return attribute.String(name, x)
	case bool:
		return attribute.Bool(name, x)
	case int:
		return attribute.Int(name, x)
	case int64:
		return attribute.Int64(name, x)
	case uint64:
		if x <= 1<<63-1 {
			return attribute.Int64(name, int64(x))
		}
		return attribute.String(name, fmt.Sprint(x))
	case time.Duration:
		return attribute.String(name, x.String())
	case json.RawMessage:
		return attribute.String(name, string(x))
	case []byte:
		return attribute.String(name, string(x))
	case error:
		return attribute.String(name, x.Error())
	case fmt.Stringer:
		return attribute.String(name, x.String())
	default:
		return attribute.String(name, fmt.Sprint(x))
	}
}

// ---- metrics ----

type instruments struct {
	request, relayCall, incoming, connect metric.Float64Histogram
	reconnects, dropped, timeouts         metric.Int64Counter
	connected                             metric.Int64UpDownCounter
}

func newInstruments(m metric.Meter) instruments {
	latency := metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300)
	var i instruments
	i.request, _ = m.Float64Histogram("walletconnect.request.duration", metric.WithUnit("s"),
		metric.WithDescription("Time from publishing a request to the peer's answer, timeout or cancellation"), latency)
	i.relayCall, _ = m.Float64Histogram("walletconnect.relay.call.duration", metric.WithUnit("s"),
		metric.WithDescription("Time from writing a relay call to its reply; a publish is confirmed only after the receiver acks"), latency)
	i.incoming, _ = m.Float64Histogram("walletconnect.incoming.duration", metric.WithUnit("s"),
		metric.WithDescription("Time from reading an incoming message to handling it"), latency)
	i.connect, _ = m.Float64Histogram("walletconnect.relay.connect.duration", metric.WithUnit("s"),
		metric.WithDescription("Time from dialing the relay to being connected"), latency)
	i.reconnects, _ = m.Int64Counter("walletconnect.relay.reconnects", metric.WithDescription("Relay reconnections, by reason"))
	i.dropped, _ = m.Int64Counter("walletconnect.incoming.dropped", metric.WithDescription("Incoming messages dropped without handling, by reason"))
	i.timeouts, _ = m.Int64Counter("walletconnect.request.timeouts", metric.WithDescription("Requests and relay calls that got no answer in time"))
	i.connected, _ = m.Int64UpDownCounter("walletconnect.relay.connected", metric.WithDescription("Open relay connections"))
	return i
}

func set(kvs ...attribute.KeyValue) metric.MeasurementOption {
	return metric.WithAttributeSet(attribute.NewSet(kvs...))
}

// RecordRequest records the wait for a peer's answer. method is the protocol
// method (wc_sessionRequest, ...); inner is the session request's own method
// (personal_sign, ...) or "".
func (t *Tracer) RecordRequest(ctx context.Context, d time.Duration, method, inner, outcome string) {
	if t == nil || t.m.request == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String(AttrRelayMethod, method), attribute.String(AttrOutcome, outcome)}
	if inner != "" {
		attrs = append(attrs, attribute.String(AttrMethod, inner))
	}
	t.m.request.Record(ctx, d.Seconds(), set(attrs...))
}

// RecordRelayCall records a relay call from first write to reply or failure.
func (t *Tracer) RecordRelayCall(ctx context.Context, d time.Duration, method, outcome string) {
	if t == nil || t.m.relayCall == nil {
		return
	}
	t.m.relayCall.Record(ctx, d.Seconds(), set(attribute.String(AttrRelayMethod, method), attribute.String(AttrOutcome, outcome)))
}

// RecordIncoming records the handling of a delivered message.
func (t *Tracer) RecordIncoming(ctx context.Context, d time.Duration, kind, outcome string) {
	if t == nil || t.m.incoming == nil {
		return
	}
	t.m.incoming.Record(ctx, d.Seconds(), set(attribute.String(AttrKind, kind), attribute.String(AttrOutcome, outcome)))
}

// RecordConnect records a dial attempt.
func (t *Tracer) RecordConnect(ctx context.Context, d time.Duration, ok bool) {
	if t == nil || t.m.connect == nil {
		return
	}
	outcome := "ok"
	if !ok {
		outcome = "failed"
	}
	t.m.connect.Record(ctx, d.Seconds(), set(attribute.String(AttrOutcome, outcome)))
}

func (t *Tracer) Reconnect(ctx context.Context, reason string) {
	if t != nil && t.m.reconnects != nil {
		t.m.reconnects.Add(ctx, 1, set(attribute.String(AttrReason, reason)))
	}
}

func (t *Tracer) Dropped(ctx context.Context, reason string) {
	if t != nil && t.m.dropped != nil {
		t.m.dropped.Add(ctx, 1, set(attribute.String(AttrReason, reason)))
	}
}

func (t *Tracer) Timeout(ctx context.Context, method string) {
	if t != nil && t.m.timeouts != nil {
		t.m.timeouts.Add(ctx, 1, set(attribute.String(AttrRelayMethod, method)))
	}
}

// Connected moves the open-connection count by delta.
func (t *Tracer) Connected(ctx context.Context, delta int64) {
	if t != nil && t.m.connected != nil {
		t.m.connected.Add(ctx, delta)
	}
}
