package trace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/tracefile"
)

// capture is an Exporter that keeps every span.
type capture struct {
	mu    sync.Mutex
	spans []*tracefile.Span
	err   error
}

func (c *capture) Export(s *tracefile.Span) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spans = append(c.spans, s)
	return c.err
}

func (c *capture) byName(t *testing.T, name string) *tracefile.Span {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.spans {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no exported span %q", name)
	return nil
}

func attrOf(attrs []tracefile.Attribute, key string) any {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value
		}
	}
	return nil
}

// With only an exporter, every span exists, is exported when it ends, and
// carries ids, attributes, events, links and status; nothing is logged.
func TestExport(t *testing.T) {
	var buf bytes.Buffer
	exp := &capture{}
	tr := New(Options{Logger: slog.New(slog.NewTextHandler(&buf, nil)), Exporter: exp, Version: "test"})

	parent, ctx := tr.Start(context.Background(), "request", "topic", "abc", "method", "personal_sign")
	child, cctx := tr.Child(ctx, "wc.peer.answer", "id", int64(42), "method", "wc_sessionRequest")
	if parent == nil || child == nil {
		t.Fatal("spans should exist with an exporter alone")
	}
	child.Step("sent", "socket", uint64(3), "took", 76*1000000)
	incoming, _ := tr.StartIncoming(context.Background(), "incoming", "relayId", "77", "topic", "abc", "tag", 1109, "acked", true)
	child.Link(incoming)
	incoming.End(nil)
	child.End(nil, "outcome", "ok")
	parent.End(errors.New("boom"))
	tr.Event("relay connected", "socket", 1)
	if From(cctx) != child {
		t.Fatal("child context should carry the child")
	}

	p, c, in := exp.byName(t, "walletconnect.request"), exp.byName(t, "wc.peer.answer"), exp.byName(t, "relay.incoming")
	if len(p.TraceID) != 32 || len(p.SpanID) != 16 || p.ParentSpanID != "" {
		t.Errorf("root ids %q %q parent %q", p.TraceID, p.SpanID, p.ParentSpanID)
	}
	if c.TraceID != p.TraceID || c.ParentSpanID != p.SpanID || c.SpanID == p.SpanID {
		t.Errorf("child ids trace %s parent %s span %s; root trace %s span %s", c.TraceID, c.ParentSpanID, c.SpanID, p.TraceID, p.SpanID)
	}
	if in.TraceID == p.TraceID || in.ParentSpanID != "" {
		t.Errorf("incoming should be its own trace: %+v", in)
	}
	if parent.ID() != p.TraceID[:8] || child.ID() != parent.ID() {
		t.Errorf("log ids %s %s, trace id %s", parent.ID(), child.ID(), p.TraceID)
	}
	if p.Kind != tracefile.KindInternal || c.Kind != tracefile.KindClient || in.Kind != tracefile.KindConsumer {
		t.Errorf("kinds %v %v %v", p.Kind, c.Kind, in.Kind)
	}
	if attrOf(p.Attributes, AttrTopic) != "abc" || attrOf(p.Attributes, AttrMethod) != "personal_sign" {
		t.Errorf("root attributes %+v", p.Attributes)
	}
	if attrOf(c.Attributes, AttrRPCID) != int64(42) || attrOf(c.Attributes, AttrRelayMethod) != "wc_sessionRequest" || attrOf(c.Attributes, AttrOutcome) != "ok" {
		t.Errorf("child attributes %+v", c.Attributes)
	}
	if attrOf(in.Attributes, AttrRelayTag) != int64(1109) || attrOf(in.Attributes, "wc.acked") != true {
		t.Errorf("incoming attributes %+v", in.Attributes)
	}
	if len(c.Events) != 1 || c.Events[0].Name != "sent" || attrOf(c.Events[0].Attributes, AttrRelaySocket) != int64(3) || c.Events[0].Time.Before(c.Start) {
		t.Errorf("child events %+v", c.Events)
	}
	if len(c.Links) != 1 || c.Links[0].TraceID != in.TraceID || c.Links[0].SpanID != in.SpanID {
		t.Errorf("child links %+v, incoming %s/%s", c.Links, in.TraceID, in.SpanID)
	}
	if p.Status.Code != tracefile.StatusError || p.Status.Message != "boom" || c.Status.Code != tracefile.StatusOK || c.Status.Message != "" {
		t.Errorf("status root %+v child %+v", p.Status, c.Status)
	}
	if !p.End.After(p.Start) || c.End.Before(c.Start) {
		t.Errorf("times root %v-%v child %v-%v", p.Start, p.End, c.Start, c.End)
	}
	if ev := exp.byName(t, "relay.connected"); attrOf(ev.Attributes, AttrRelaySocket) != int64(1) {
		t.Errorf("event span %+v", ev)
	}
	if buf.Len() != 0 {
		t.Errorf("nothing should be logged without LogLines:\n%s", buf.String())
	}
}

// Steps after End still log but are not exported, and the record is not
// touched again.
func TestExportLateStep(t *testing.T) {
	exp := &capture{}
	tr := New(Options{Exporter: exp})
	s, _ := tr.Start(context.Background(), "request")
	s.End(nil)
	s.Step("peer answered", "outcome", "ok")
	s.Link(s)
	rec := exp.byName(t, "walletconnect.request")
	if len(rec.Events) != 0 || len(rec.Links) != 0 {
		t.Errorf("late step or link exported: %+v", rec)
	}
}

// The peer's error answer ends the span as done with its code; the same kind
// of error from elsewhere, such as the relay, still fails it.
func TestAnsweredIsDone(t *testing.T) {
	exp := &capture{}
	tr := New(Options{Exporter: exp})
	answered, _ := tr.Start(context.Background(), "request")
	rejection := &jsonrpc.Error{Code: 5000, Message: "User rejected."}
	answered.Answered(rejection)
	answered.End(fmt.Errorf("wrapped: %w", rejection))
	failed, _ := tr.Start(context.Background(), "ping")
	failed.Answered(rejection)
	failed.End(&jsonrpc.Error{Code: 5000, Message: "relay"})

	a, f := exp.byName(t, "walletconnect.request"), exp.byName(t, "walletconnect.ping")
	if a.Status.Code != tracefile.StatusOK || attrOf(a.Attributes, "wc.error_code") != int64(5000) {
		t.Errorf("answered span status %+v attributes %+v", a.Status, a.Attributes)
	}
	if f.Status.Code != tracefile.StatusError {
		t.Errorf("failed span status %+v attributes %+v", f.Status, f.Attributes)
	}
}

// With OpenTelemetry on as well, the exported span carries OpenTelemetry's
// ids, and a root started inside an application span is its child.
func TestExportMatchesOpenTelemetryIDs(t *testing.T) {
	otel := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(otel))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	exp := &capture{}
	tr := New(Options{Exporter: exp, TracerProvider: tp})

	appCtx, appSpan := tp.Tracer("app").Start(context.Background(), "app.op")
	parent, ctx := tr.Start(appCtx, "request")
	child, _ := tr.Child(ctx, "wc.peer.answer")
	child.End(nil)
	parent.End(nil)
	appSpan.End()

	p, c := exp.byName(t, "walletconnect.request"), exp.byName(t, "wc.peer.answer")
	op, oc := spanByName(t, otel, "walletconnect.request"), spanByName(t, otel, "wc.peer.answer")
	if p.TraceID != op.SpanContext.TraceID().String() || p.SpanID != op.SpanContext.SpanID().String() {
		t.Errorf("root ids %s/%s, OpenTelemetry %v", p.TraceID, p.SpanID, op.SpanContext)
	}
	if p.ParentSpanID != appSpan.SpanContext().SpanID().String() {
		t.Errorf("root parent %s, app span %s", p.ParentSpanID, appSpan.SpanContext().SpanID())
	}
	if c.SpanID != oc.SpanContext.SpanID().String() || c.ParentSpanID != p.SpanID {
		t.Errorf("child ids %s parent %s, OpenTelemetry %v", c.SpanID, c.ParentSpanID, oc.SpanContext)
	}
}

// A sampled-out OpenTelemetry span still exports, under the ids the
// sampler assigned.
func TestExportWhenNotSampled(t *testing.T) {
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
	exp := &capture{}
	tr := New(Options{Exporter: exp, TracerProvider: tp})
	s, ctx := tr.Start(context.Background(), "ping", "topic", "t")
	c, _ := tr.Child(ctx, "wc.peer.answer")
	c.End(nil)
	s.End(nil)
	p := exp.byName(t, "walletconnect.ping")
	if len(p.TraceID) != 32 || attrOf(p.Attributes, AttrTopic) != "t" || exp.byName(t, "wc.peer.answer").ParentSpanID != p.SpanID {
		t.Errorf("exported %+v", exp.spans)
	}
}

func TestReconnectExported(t *testing.T) {
	var buf bytes.Buffer
	exp := &capture{}
	tr := New(Options{Logger: slog.New(slog.NewTextHandler(&buf, nil)), LogLines: true, Exporter: exp})
	tr.Reconnect(context.Background(), "connection_lost")
	if s := exp.byName(t, "relay.reconnect"); attrOf(s.Attributes, AttrReason) != "connection_lost" || s.Status.Code != tracefile.StatusOK {
		t.Errorf("reconnect span %+v", s)
	}
	if strings.Contains(buf.String(), "reconnect") {
		t.Errorf("reconnect should add no log line:\n%s", buf.String())
	}
	New(Options{}).Reconnect(context.Background(), "requested") // every sink off: nothing happens
}

func TestExportFailureReportedOnce(t *testing.T) {
	var buf bytes.Buffer
	exp := &capture{err: errors.New("disk full")}
	tr := New(Options{Logger: slog.New(slog.NewTextHandler(&buf, nil)), Exporter: exp})
	for range 3 {
		tr.Event("x")
	}
	if n := strings.Count(buf.String(), "disk full"); n != 1 {
		t.Fatalf("failure logged %d times:\n%s", n, buf.String())
	}
}

const testJWT = "eyJhbGciOiJFZERTQSIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJkaWQ6a2V5OnoifQ.c2lnbmF0dXJlLXNpZ25hdHVyZS1zaWduYXR1cmU"

// A token that reaches a span in an error or an attribute is scrubbed from
// the exported record and from the OpenTelemetry span alike.
func TestScrubTokens(t *testing.T) {
	exp := &capture{}
	tr, otel, _ := otelSinks(t, nil, false)
	tr.exp = exp
	dialErr := errors.New(`failed to WebSocket dial: Get "https://relay/?auth=` + testJWT + `&projectId=x": refused`)
	s, _ := tr.Start(context.Background(), "relay connect", "relayError", "token "+testJWT+" in an attribute")
	s.Step("retry", "err", dialErr)
	s.End(dialErr)

	rec := exp.byName(t, "relay.connect")
	if strings.Contains(rec.Status.Message, testJWT) || !strings.Contains(rec.Status.Message, "auth=<jwt>&projectId=x") {
		t.Errorf("file status %q", rec.Status.Message)
	}
	if v, _ := attrOf(rec.Attributes, "relay.error").(string); strings.Contains(v, testJWT) || !strings.Contains(v, "token <jwt> in") {
		t.Errorf("file attribute %q", v)
	}
	if v, _ := attrOf(rec.Events[0].Attributes, "error.message").(string); strings.Contains(v, testJWT) {
		t.Errorf("file event attribute %q", v)
	}
	osp := spanByName(t, otel, "relay.connect")
	if strings.Contains(osp.Status.Description, testJWT) || strings.Contains(attrValue(osp.Attributes, "relay.error"), testJWT) {
		t.Errorf("OpenTelemetry span still holds the token: %+v", osp)
	}
	for _, ev := range osp.Events {
		for _, a := range ev.Attributes {
			if strings.Contains(a.Value.AsString(), testJWT) {
				t.Errorf("OpenTelemetry event %s attribute %s holds the token", ev.Name, a.Key)
			}
		}
	}
	if scrub("a topic 0123456789abcdef") != "a topic 0123456789abcdef" {
		t.Error("scrub changed a string without a token")
	}
}
