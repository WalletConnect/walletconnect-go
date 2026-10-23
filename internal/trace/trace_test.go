package trace

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// otelSinks returns a tracer recording into an exporter and a metric reader.
func otelSinks(t *testing.T, log *slog.Logger, logLines bool) (*Tracer, *tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()); _ = mp.Shutdown(context.Background()) })
	return New(Options{Logger: log, LogLines: logLines, TracerProvider: tp, MeterProvider: mp, Version: "test"}), exp, reader
}

func spanByName(t *testing.T, exp *tracetest.InMemoryExporter, name string) tracetest.SpanStub {
	t.Helper()
	for _, s := range exp.GetSpans() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no span %q; have %v", name, spanNames(exp))
	return tracetest.SpanStub{}
}

func spanNames(exp *tracetest.InMemoryExporter) []string {
	var out []string
	for _, s := range exp.GetSpans() {
		out = append(out, s.Name)
	}
	return out
}

func attrValue(attrs []attribute.KeyValue, key string) string {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.String()
		}
	}
	return ""
}

func TestSpanLogLines(t *testing.T) {
	var buf bytes.Buffer
	tr := New(Options{Logger: slog.New(slog.NewTextHandler(&buf, nil)), LogLines: true, TracerProvider: noop.NewTracerProvider()})
	s, ctx := tr.Start(context.Background(), "request", "method", "personal_sign")
	From(ctx).Step("relay send", "id", 7)
	s.End(errors.New("boom"))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines:\n%s", len(lines), buf.String())
	}
	for i, want := range []string{"step=start", `step="relay send"`, "step=failed"} {
		if !strings.Contains(lines[i], "trace="+s.ID()) || !strings.Contains(lines[i], want) {
			t.Errorf("line %d = %s, want trace=%s and %s", i, lines[i], s.ID(), want)
		}
	}
	if !strings.Contains(lines[2], "err=boom") {
		t.Errorf("end line lacks error: %s", lines[2])
	}
}

func TestOff(t *testing.T) {
	tr := New(Options{Logger: slog.Default(), LogLines: false, TracerProvider: noop.NewTracerProvider()})
	s, ctx := tr.Start(context.Background(), "x")
	s.Step("y")
	s.End(nil)
	tr.Event("z")
	if s != nil || s.ID() != "" || From(ctx) != nil {
		t.Fatal("tracing off should produce nil spans")
	}
	var nilTracer *Tracer
	if s, _ := nilTracer.Start(context.Background(), "x"); s != nil {
		t.Fatal("nil tracer should produce nil spans")
	}
	if n := testing.AllocsPerRun(100, func() { nilTracer.Start(context.Background(), "x", "k", "v") }); n != 0 {
		t.Fatalf("nil tracer allocates %.0f per Start", n)
	}
}

// With every sink off, Start never touches OpenTelemetry or converts
// attributes: it allocates nothing and returns nil.
func TestOffIsFree(t *testing.T) {
	tr := New(Options{})
	ctx := context.Background()
	if n := testing.AllocsPerRun(100, func() { tr.Start(ctx, "request", "topic", "t", "id", int64(1)) }); n != 0 {
		t.Fatalf("Start with every sink off allocates %.0f", n)
	}
	if s, _ := tr.Child(ctx, "x"); s != nil {
		t.Fatal("child without a provider should be nil")
	}
}

func TestOpenTelemetrySpans(t *testing.T) {
	var buf bytes.Buffer
	tr, exp, _ := otelSinks(t, slog.New(slog.NewTextHandler(&buf, nil)), true)

	parent, ctx := tr.Start(context.Background(), "request", "topic", "abc", "chainId", "eip155:1", "method", "personal_sign")
	child, cctx := tr.Child(ctx, "wc.peer.answer", "id", int64(42), "method", "wc_sessionRequest")
	child.Step("sent", "socket", uint64(3))
	incoming, _ := tr.StartIncoming(context.Background(), "incoming", "relayId", "77", "topic", "abc", "tag", 1109)
	child.Link(incoming)
	child.End(nil, "outcome", "ok")
	incoming.End(nil, "acked", true)
	parent.End(errors.New("boom"), "took", 1500*time.Millisecond)

	p := spanByName(t, exp, "walletconnect.request")
	c := spanByName(t, exp, "wc.peer.answer")
	in := spanByName(t, exp, "relay.incoming")

	// names, kinds and parentage
	if c.Parent.SpanID() != p.SpanContext.SpanID() {
		t.Fatalf("child parent %s, want %s", c.Parent.SpanID(), p.SpanContext.SpanID())
	}
	if c.SpanKind != oteltrace.SpanKindClient || in.SpanKind != oteltrace.SpanKindConsumer || p.SpanKind != oteltrace.SpanKindInternal {
		t.Fatalf("kinds: parent %v child %v incoming %v", p.SpanKind, c.SpanKind, in.SpanKind)
	}
	// attributes mapped to stable names
	for _, c := range []struct {
		span tracetest.SpanStub
		key  string
		want string
	}{
		{p, AttrTopic, "abc"}, {p, AttrChainID, "eip155:1"}, {p, AttrMethod, "personal_sign"}, {p, "relay.took", "1.5s"},
		{c, AttrRPCID, "42"}, {c, AttrRelayMethod, "wc_sessionRequest"}, {c, AttrOutcome, "ok"},
		{in, AttrRelayID, "77"}, {in, AttrRelayTag, "1109"}, {in, "wc.acked", "true"},
	} {
		if got := attrValue(c.span.Attributes, c.key); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.span.Name, c.key, got, c.want)
		}
	}
	// events, status, links
	if len(c.Events) != 1 || c.Events[0].Name != "sent" || attrValue(c.Events[0].Attributes, AttrRelaySocket) != "3" {
		t.Fatalf("child events %+v", c.Events)
	}
	if p.Status.Code != codes.Error || p.Status.Description != "boom" || c.Status.Code != codes.Ok {
		t.Fatalf("status: parent %+v child %+v", p.Status, c.Status)
	}
	if len(c.Links) != 1 || c.Links[0].SpanContext.SpanID() != in.SpanContext.SpanID() {
		t.Fatalf("child links %+v, want the incoming span", c.Links)
	}
	// the log trace id is the OpenTelemetry trace id's prefix; the child shares it but logs nothing
	if parent.ID() != p.SpanContext.TraceID().String()[:8] {
		t.Fatalf("log id %s, want prefix of %s", parent.ID(), p.SpanContext.TraceID())
	}
	if child.ID() != parent.ID() || From(cctx) != child {
		t.Fatalf("child id %s, parent %s", child.ID(), parent.ID())
	}
	if strings.Contains(buf.String(), "peer.answer") {
		t.Fatalf("child span wrote log lines:\n%s", buf.String())
	}
	if n := strings.Count(buf.String(), "trace="+parent.ID()); n != 2 {
		t.Fatalf("parent wrote %d lines, want 2 (start, failed):\n%s", n, buf.String())
	}
	if n := strings.Count(buf.String(), "trace="+incoming.ID()); n != 2 || incoming.ID() != in.SpanContext.TraceID().String()[:8] {
		t.Fatalf("incoming wrote %d lines with id %s (otel trace %s):\n%s", n, incoming.ID(), in.SpanContext.TraceID(), buf.String())
	}
}

// With log lines on but no recording provider, a child still shares its
// parent's log id, so later Steps on the parent stay readable.
func TestChildInheritsLogIDWithoutOTel(t *testing.T) {
	tr := New(Options{Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), LogLines: true, TracerProvider: noop.NewTracerProvider()})
	parent, ctx := tr.Start(context.Background(), "request")
	child, _ := tr.Child(ctx, "wc.peer.answer")
	if child != nil {
		t.Fatalf("child should be nil without a recording provider, got %v", child)
	}
	if parent.ID() == "" {
		t.Fatal("parent has no id")
	}
}

func TestSpanName(t *testing.T) {
	for in, want := range map[string]string{
		"request": "walletconnect.request", "respond error": "walletconnect.respond_error", "incoming": "relay.incoming",
		"relay connection lost": "relay.connection_lost", "wc.peer.answer": "wc.peer.answer", "relay.publish": "relay.publish",
	} {
		if got := spanName(in); got != want {
			t.Errorf("spanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAttributeMapping(t *testing.T) {
	got := kv([]any{"method", "irn_publish", "method", "eth_sign", "relayId", int64(5), "unknownKey", 1, "already.dotted", "x", "err", errors.New("e")})
	want := map[string]string{"rpc.method": "irn_publish", "wc.method": "eth_sign", "relay.id": "5", "wc.unknownKey": "1", "already.dotted": "x", "error.message": "e"}
	if len(got) != len(want) {
		t.Fatalf("got %d attributes %v", len(got), got)
	}
	for _, a := range got {
		if want[string(a.Key)] != a.Value.String() {
			t.Errorf("%s = %s, want %s", a.Key, a.Value.String(), want[string(a.Key)])
		}
	}
}

func TestMetrics(t *testing.T) {
	tr, _, reader := otelSinks(t, nil, false)
	ctx := context.Background()
	tr.RecordRequest(ctx, 1500*time.Millisecond, "wc_sessionRequest", "personal_sign", "ok")
	tr.RecordRelayCall(ctx, 80*time.Millisecond, "irn_publish", "ok")
	tr.RecordIncoming(ctx, 5*time.Millisecond, "response", "handled")
	tr.RecordConnect(ctx, 300*time.Millisecond, true)
	tr.Reconnect(ctx, "ping_failed")
	tr.Dropped(ctx, "unknown_topic")
	tr.Timeout(ctx, "wc_sessionRequest")
	tr.Connected(ctx, 1)
	tr.Connected(ctx, -1)
	var nilTracer *Tracer
	nilTracer.RecordRequest(ctx, time.Second, "x", "", "ok") // must not panic

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != Scope {
			t.Fatalf("scope %q, want %q", sm.Scope.Name, Scope)
		}
		for _, m := range sm.Metrics {
			found[m.Name] = true
			switch m.Name {
			case "walletconnect.request.duration":
				h := m.Data.(metricdata.Histogram[float64])
				if len(h.DataPoints) != 1 || h.DataPoints[0].Count != 1 || h.DataPoints[0].Sum != 1.5 {
					t.Fatalf("request histogram %+v", h.DataPoints)
				}
				if v, _ := h.DataPoints[0].Attributes.Value(AttrMethod); v.AsString() != "personal_sign" {
					t.Fatalf("request attributes %v", h.DataPoints[0].Attributes)
				}
			case "walletconnect.relay.connected":
				s := m.Data.(metricdata.Sum[int64])
				if len(s.DataPoints) != 1 || s.DataPoints[0].Value != 0 {
					t.Fatalf("connected %+v", s.DataPoints)
				}
			}
		}
	}
	for _, name := range []string{"walletconnect.request.duration", "walletconnect.relay.call.duration", "walletconnect.incoming.duration",
		"walletconnect.relay.connect.duration", "walletconnect.relay.reconnects", "walletconnect.incoming.dropped",
		"walletconnect.request.timeouts", "walletconnect.relay.connected"} {
		if !found[name] {
			t.Errorf("metric %s not exported; have %v", name, found)
		}
	}
}
