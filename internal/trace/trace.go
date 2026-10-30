// Package trace follows one action, such as a request or an incoming relay
// message, from start to finish, and reports it three ways.
//
// Trace lines go to a logger: every line of a span shares its trace ID and
// reports the time since the span started, so an action can be read in order
// across the API, the engine and the relay transport.
//
// Spans go to an Exporter, normally the trace file the client keeps beside
// its database, and to the OpenTelemetry providers the application installed.
// Each action, relay call and incoming message becomes a span carrying the
// identifiers needed to match it with relay-side logs (topic, relay JSON-RPC
// id, payload id, tag). Child spans measure one part of an action, such as
// the wait for the peer's answer; they are exported but add no log lines.
// Latencies also go to OpenTelemetry as histograms with bounded attributes.
//
// A nil *Tracer or *Span is valid and does nothing. With no logger, no
// exporter and no provider, Start returns nil spans without touching
// OpenTelemetry, so tracing costs nothing when it is off; with a provider
// that does not record, it costs one span start.
package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/tracefile"
)

// Scope is the OpenTelemetry instrumentation scope of every span and metric.
const Scope = "github.com/WalletConnect/walletconnect-go"

// An Exporter receives every span when it ends. The trace file is one.
type Exporter interface {
	Export(*tracefile.Span) error
}

type Options struct {
	// Logger receives trace lines at info level when LogLines is set, and
	// a warning if the Exporter fails.
	Logger   *slog.Logger
	LogLines bool
	// Exporter receives every span; nil turns it off.
	Exporter Exporter
	// TracerProvider and MeterProvider receive spans and metrics; nil turns
	// each off. Pass otel.GetTracerProvider() to use the globals.
	TracerProvider oteltrace.TracerProvider
	MeterProvider  metric.MeterProvider
	// Version is reported as the instrumentation version.
	Version string
}

type Tracer struct {
	log    *slog.Logger     // nil when trace lines are off
	errlog *slog.Logger     // for an exporter failure, reported once
	exp    Exporter         // nil when exporting is off
	tracer oteltrace.Tracer // nil when OpenTelemetry tracing is off
	m      instruments      // zero when OpenTelemetry metrics are off

	exportFailed sync.Once
}

// New returns a tracer for the given sinks. A tracer whose sinks are all
// off still works; its spans are nil.
func New(o Options) *Tracer {
	t := &Tracer{exp: o.Exporter, errlog: o.Logger}
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
	t       *Tracer
	id      string // the log trace ID: the first eight hex characters of traceID
	traceID oteltrace.TraceID
	spanID  oteltrace.SpanID
	action  string
	start   time.Time
	sp      oteltrace.Span // nil when OpenTelemetry tracing is off
	quiet   bool           // a child span: exported only, no log lines

	mu     sync.Mutex
	rec    *tracefile.Span // the record being built for the exporter; nil when off or after End
	answer *jsonrpc.Error  // the peer's error answer, see Answered
}

func (s *Span) recording() bool { return s.sp != nil && s.sp.IsRecording() }

// exporting reports whether the span still has a record to export; End
// takes it away, possibly on another goroutine than a late Step.
func (s *Span) exporting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec != nil
}

// Start opens a span for an action and logs its first line. The returned
// context carries the span, so spans started from it become its children.
func (t *Tracer) Start(ctx context.Context, action string, attrs ...any) (*Span, context.Context) {
	return t.start(ctx, oteltrace.SpanKindInternal, action, false, attrs)
}

// StartIncoming opens a span for a message delivered by the relay.
func (t *Tracer) StartIncoming(ctx context.Context, action string, attrs ...any) (*Span, context.Context) {
	return t.start(ctx, oteltrace.SpanKindConsumer, action, false, attrs)
}

// Child opens a span under the span ctx carries, for a part of an action
// with its own duration. It is exported like any span but writes no log
// lines. The span is nil when nothing records, so callers need no checks.
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
	if t.tracer == nil && t.exp == nil && (quiet || t.log == nil) {
		return nil, ctx
	}
	parent := From(ctx)
	var sp oteltrace.Span
	var otelParent oteltrace.SpanContext
	var kvs []attribute.KeyValue
	if t.tracer != nil {
		otelParent = oteltrace.SpanContextFromContext(ctx)
		ctx, sp = t.tracer.Start(ctx, spanName(action), spanKinds[kind])
		if !sp.IsRecording() {
			if t.exp == nil && (quiet || t.log == nil) {
				sp.End()
				return nil, ctx
			}
		} else {
			kvs = kv(attrs)
			sp.SetAttributes(kvs...)
		}
	}
	s := &Span{t: t, action: action, start: time.Now(), sp: sp, quiet: quiet}
	// Identifiers: OpenTelemetry's when it issued them, so the file and a
	// backend name the same span; otherwise the parent's trace, or a new one.
	var parentID oteltrace.SpanID
	switch {
	case sp != nil && sp.SpanContext().HasTraceID():
		sc := sp.SpanContext()
		s.traceID, s.spanID = sc.TraceID(), sc.SpanID()
		if parent != nil {
			parentID = parent.spanID
		} else if otelParent.IsValid() {
			parentID = otelParent.SpanID()
		}
	case parent != nil:
		s.traceID, parentID = parent.traceID, parent.spanID
		_, _ = rand.Read(s.spanID[:])
	default:
		_, _ = rand.Read(s.traceID[:])
		_, _ = rand.Read(s.spanID[:])
	}
	s.id = hex.EncodeToString(s.traceID[:4])
	if t.exp != nil {
		if kvs == nil {
			kvs = kv(attrs)
		}
		s.rec = &tracefile.Span{TraceID: s.traceID.String(), SpanID: s.spanID.String(), Name: spanName(action),
			Kind: fileKinds[kind], Start: s.start, Attributes: fileAttrs(nil, kvs)}
		if parentID.IsValid() {
			s.rec.ParentSpanID = parentID.String()
		}
	}
	ctx = context.WithValue(ctx, ctxKey{}, s)
	if !quiet {
		s.emit("start", nil, attrs)
	}
	return s, ctx
}

// Step logs progress and adds an event to the span. Steps may follow End on
// the log side: a peer answer can arrive after the API call that sent the
// request has returned. Such late steps are not exported.
func (s *Span) Step(step string, attrs ...any) {
	if s == nil {
		return
	}
	s.emit(step, nil, attrs)
	if !s.recording() && !s.exporting() {
		return
	}
	kvs := kv(attrs)
	if s.recording() {
		s.sp.AddEvent(step, oteltrace.WithAttributes(kvs...))
	}
	s.mu.Lock()
	if s.rec != nil {
		s.rec.Events = append(s.rec.Events, tracefile.Event{Name: step, Time: time.Now(), Attributes: fileAttrs(nil, kvs)})
	}
	s.mu.Unlock()
}

// Answered records err as the peer's answer to the action, such as a user
// rejecting a request. The action did its part: when End gets this error,
// the span ends as done, with the error's code, not as failed.
func (s *Span) Answered(err *jsonrpc.Error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.answer = err
	s.mu.Unlock()
}

// End logs the outcome, "done" or "failed" with err, ends the OpenTelemetry
// span with the matching status and exports the span. The peer's error
// answer (see Answered) is done.
func (s *Span) End(err error, attrs ...any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.answer != nil && errors.Is(err, s.answer) {
		err, attrs = nil, append(attrs, "code", s.answer.Code)
	}
	s.mu.Unlock()
	if err != nil {
		s.emit("failed", err, attrs)
	} else {
		s.emit("done", nil, attrs)
	}
	var kvs []attribute.KeyValue
	if len(attrs) > 0 && (s.recording() || s.exporting()) {
		kvs = kv(attrs)
	}
	if s.recording() {
		if len(kvs) > 0 {
			s.sp.SetAttributes(kvs...)
		}
		if err != nil {
			s.sp.RecordError(scrubErr(err))
			s.sp.SetStatus(codes.Error, scrub(err.Error()))
		} else {
			s.sp.SetStatus(codes.Ok, "")
		}
	}
	if s.sp != nil {
		s.sp.End()
	}
	s.mu.Lock()
	rec := s.rec
	s.rec = nil
	s.mu.Unlock()
	if rec != nil {
		rec.End = time.Now()
		rec.Attributes = fileAttrs(rec.Attributes, kvs)
		if err != nil {
			rec.Status = tracefile.Status{Code: tracefile.StatusError, Message: scrub(err.Error())}
		} else {
			rec.Status = tracefile.Status{Code: tracefile.StatusOK}
		}
		s.t.export(rec)
	}
}

// Link records that other, usually the incoming message that answered this
// span's request, belongs to this span's story.
func (s *Span) Link(other *Span) {
	if s == nil || other == nil {
		return
	}
	if other.sp != nil && s.recording() {
		s.sp.AddLink(oteltrace.Link{SpanContext: other.sp.SpanContext()})
	}
	s.mu.Lock()
	if s.rec != nil {
		s.rec.Links = append(s.rec.Links, tracefile.Link{TraceID: other.traceID.String(), SpanID: other.spanID.String()})
	}
	s.mu.Unlock()
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

// export hands a finished span to the exporter. A failing exporter is
// reported once, then ignored: the trace file must never fail the client.
func (t *Tracer) export(rec *tracefile.Span) {
	if err := t.exp.Export(rec); err != nil {
		t.exportFailed.Do(func() {
			if t.errlog != nil {
				t.errlog.Warn("trace export failed; later failures are not logged", "err", err)
			}
		})
	}
}

// fileAttrs appends kvs to attrs as trace file attributes.
func fileAttrs(attrs []tracefile.Attribute, kvs []attribute.KeyValue) []tracefile.Attribute {
	for _, kv := range kvs {
		a := tracefile.Attribute{Key: string(kv.Key)}
		switch kv.Value.Type() {
		case attribute.STRING:
			a.Value = kv.Value.AsString()
		case attribute.BOOL:
			a.Value = kv.Value.AsBool()
		case attribute.INT64:
			a.Value = kv.Value.AsInt64()
		case attribute.FLOAT64:
			a.Value = kv.Value.AsFloat64()
		default:
			a.Value = kv.Value.String()
		}
		attrs = append(attrs, a)
	}
	return attrs
}

// spanKinds are preallocated so a non-recording start allocates nothing for
// its options.
var spanKinds = map[oteltrace.SpanKind]oteltrace.SpanStartOption{
	oteltrace.SpanKindInternal: oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
	oteltrace.SpanKindClient:   oteltrace.WithSpanKind(oteltrace.SpanKindClient),
	oteltrace.SpanKindConsumer: oteltrace.WithSpanKind(oteltrace.SpanKindConsumer),
}

var fileKinds = map[oteltrace.SpanKind]tracefile.Kind{
	oteltrace.SpanKindInternal: tracefile.KindInternal,
	oteltrace.SpanKindClient:   tracefile.KindClient,
	oteltrace.SpanKindConsumer: tracefile.KindConsumer,
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

// jwtRe matches a JSON Web Token. A dial error quotes the relay URL, whose
// query carries the relay auth token; the relay redacts that at the source,
// and scrub keeps any token that reaches a span by another path out of
// exported attributes and statuses.
var jwtRe = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

func scrub(s string) string {
	if !strings.Contains(s, "eyJ") {
		return s
	}
	return jwtRe.ReplaceAllString(s, "<jwt>")
}

// scrubErr returns err, or an error with the scrubbed message when the
// message held a token.
func scrubErr(err error) error {
	if msg := err.Error(); scrub(msg) != msg {
		return errors.New(scrub(msg))
	}
	return err
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
		return attribute.String(name, scrub(x))
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
		return attribute.String(name, scrub(x.Error()))
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

// Reconnect counts a reconnection and exports it as a relay.reconnect span
// with the reason, so a trace file shows them without a metrics backend.
func (t *Tracer) Reconnect(ctx context.Context, reason string) {
	if t == nil {
		return
	}
	if t.m.reconnects != nil {
		t.m.reconnects.Add(ctx, 1, set(attribute.String(AttrReason, reason)))
	}
	if s, _ := t.start(ctx, oteltrace.SpanKindInternal, "relay.reconnect", true, []any{"reason", reason}); s != nil {
		s.End(nil)
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
