package engine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/trace"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// A session request from the dApp side, answered by the wallet: the API span
// gets a wc.peer.answer child linked to the incoming answer, and the request
// latency histogram gets one point.
func TestRequestTracing(t *testing.T) {
	h := newHarness(t)
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()); _ = mp.Shutdown(context.Background()) })
	h.e.tr = trace.New(trace.Options{TracerProvider: tp, MeterProvider: mp})

	topic, symHex := h.seedSession(false)
	parent, ctx := h.e.tr.Start(context.Background(), "request", "topic", topic)
	type res struct {
		raw json.RawMessage
		err error
	}
	done := make(chan res, 1)
	go func() {
		raw, err := h.e.Request(ctx, RequestParams{Topic: topic, ChainID: "eip155:1", Method: "personal_sign",
			Params: json.RawMessage(`["0x68656c6c6f","` + testAddress + `"]`)})
		done <- res{raw, err}
	}()
	var id int64
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline) && id == 0; {
		if len(h.relay.publishes(topic)) == 1 {
			msg, _ := h.reply(topic, symHex)
			id = msg.ID
		} else {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if id == 0 {
		t.Fatal("request was not published")
	}

	msg, err := encrypt(symHex, resultPayload(id, "0xsignature"))
	if err != nil {
		t.Fatal(err)
	}
	in := relayIncoming(topic, msg)
	in.Span, _ = h.e.tr.StartIncoming(context.Background(), "incoming", "relayId", string(in.ID), "topic", topic, "tag", 1109)
	if !h.e.handle(trace.With(context.Background(), in.Span), in) {
		t.Fatal("answer was not acked")
	}
	in.Span.End(nil, "acked", true)
	select {
	case r := <-done:
		if r.err != nil || string(r.raw) != `"0xsignature"` {
			t.Fatalf("Request = %s, %v", r.raw, r.err)
		}
		parent.End(r.err)
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not return")
	}

	spans := map[string]tracetest.SpanStub{}
	for _, s := range exp.GetSpans() {
		spans[s.Name] = s
	}
	p, c, i := spans["walletconnect.request"], spans["wc.peer.answer"], spans["relay.incoming"]
	if p.Name == "" || c.Name == "" || i.Name == "" {
		t.Fatalf("spans %v", spans)
	}
	if c.Parent.SpanID() != p.SpanContext.SpanID() {
		t.Fatal("wc.peer.answer is not a child of the request span")
	}
	if c.Status.Code != codes.Ok {
		t.Fatalf("answer status %+v", c.Status)
	}
	for key, want := range map[string]string{
		trace.AttrRPCID: json.Number(jsonInt(id)).String(), trace.AttrRelayMethod: "wc_sessionRequest",
		trace.AttrMethod: "personal_sign", trace.AttrTopic: topic, trace.AttrOutcome: "ok",
	} {
		if got := attrOf(c.Attributes, key); got != want {
			t.Errorf("answer %s = %q, want %q", key, got, want)
		}
	}
	if len(c.Links) != 1 || c.Links[0].SpanContext.SpanID() != i.SpanContext.SpanID() {
		t.Fatalf("answer links %+v, want the incoming span", c.Links)
	}
	if attrOf(i.Attributes, trace.AttrRelayID) != string(in.ID) || attrOf(i.Attributes, trace.AttrTopic) != topic {
		t.Fatalf("incoming attributes %v", i.Attributes)
	}

	got := histograms(t, reader)
	req := got["walletconnect.request.duration"]
	if len(req.DataPoints) != 1 || req.DataPoints[0].Count != 1 {
		t.Fatalf("request.duration points %+v", req.DataPoints)
	}
	if v, _ := req.DataPoints[0].Attributes.Value(trace.AttrOutcome); v.AsString() != "ok" {
		t.Fatalf("request.duration attributes %v", req.DataPoints[0].Attributes)
	}
	if v, _ := req.DataPoints[0].Attributes.Value(trace.AttrMethod); v.AsString() != "personal_sign" {
		t.Fatalf("request.duration attributes %v", req.DataPoints[0].Attributes)
	}
	inc := got["walletconnect.incoming.duration"]
	if len(inc.DataPoints) != 1 {
		t.Fatalf("incoming.duration points %+v", inc.DataPoints)
	}
	if v, _ := inc.DataPoints[0].Attributes.Value(trace.AttrKind); v.AsString() != "response" {
		t.Fatalf("incoming.duration attributes %v", inc.DataPoints[0].Attributes)
	}
}

// otelSinks points the harness's tracer at an in-memory span exporter and a
// manual metric reader.
func otelSinks(t *testing.T, h *harness) (*tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()); _ = mp.Shutdown(context.Background()) })
	h.e.tr = trace.New(trace.Options{TracerProvider: tp, MeterProvider: mp})
	return exp, reader
}

// histograms collects the reader's float histograms by name.
func histograms(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Histogram[float64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]metricdata.Histogram[float64]{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if hist, ok := m.Data.(metricdata.Histogram[float64]); ok {
				got[m.Name] = hist
			}
		}
	}
	return got
}

func pointAttr(p metricdata.HistogramDataPoint[float64], key string) string {
	v, _ := p.Attributes.Value(attribute.Key(key))
	return v.AsString()
}

// The incoming latency metric does not depend on a span. With a meter
// provider alone the span is nil, and the duration counts from the time the
// relay read the frame.
func TestIncomingLatencyWithoutSpans(t *testing.T) {
	h := newHarness(t)
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	h.e.tr = trace.New(trace.Options{MeterProvider: mp})

	in := relayIncoming("unknown-topic", "not for us")
	in.Received = time.Now().Add(-50 * time.Millisecond)
	if in.Span != nil {
		t.Fatal("no span expected with a meter provider alone")
	}
	if !h.e.handle(context.Background(), in) {
		t.Fatal("a message on an unknown topic is dropped and acked")
	}
	hist := histograms(t, reader)["walletconnect.incoming.duration"]
	if len(hist.DataPoints) != 1 {
		t.Fatalf("incoming.duration points %+v, want one", hist.DataPoints)
	}
	if p := hist.DataPoints[0]; p.Sum < 0.05 || pointAttr(p, trace.AttrOutcome) != "dropped" {
		t.Fatalf("incoming.duration point sum %v attributes %v, want at least 50ms and outcome dropped", p.Sum, p.Attributes)
	}
}

// A publish failure settles the waiter: the wc.peer.answer span ends with
// the error and the request metric records publish_failed, for the dApp's
// proposal and the wallet's settlement alike.
func TestPublishFailureSettlesWaiter(t *testing.T) {
	cases := []struct {
		name, method string
		run          func(h *harness) error
	}{
		{"propose", methodPropose, func(h *harness) error {
			h.relay.failOn("wc_proposeSession", errRelay)
			_, err := h.e.Connect(h.ctx(), evmConnect())
			return err
		}},
		{"settle", methodSettle, func(h *harness) error {
			_, _, id := h.receiveProposal(nil)
			h.relay.failOn("wc_approveSession", errRelay)
			_, err := h.e.Approve(h.ctx(), ApproveParams{ProposalID: id, Namespaces: evmSession("eip155:1")})
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			exp, reader := otelSinks(t, h)
			if err := c.run(h); !errors.Is(err, errRelay) {
				t.Fatalf("got %v, want the relay error", err)
			}
			if h.e.Stats().Waiters != 0 {
				t.Fatal("waiter left behind")
			}
			var answer *tracetest.SpanStub
			for _, s := range exp.GetSpans() {
				if s.Name == "wc.peer.answer" {
					answer = &s
				}
			}
			if answer == nil {
				t.Fatal("wc.peer.answer span never ended")
			}
			if answer.Status.Code != codes.Error || attrOf(answer.Attributes, trace.AttrOutcome) != "publish_failed" ||
				attrOf(answer.Attributes, trace.AttrRelayMethod) != c.method {
				t.Fatalf("answer status %+v attributes %v", answer.Status, answer.Attributes)
			}
			req := histograms(t, reader)["walletconnect.request.duration"]
			if len(req.DataPoints) != 1 {
				t.Fatalf("request.duration points %+v, want one", req.DataPoints)
			}
			if p := req.DataPoints[0]; pointAttr(p, trace.AttrOutcome) != "publish_failed" || pointAttr(p, trace.AttrRelayMethod) != c.method {
				t.Fatalf("request.duration attributes %v", p.Attributes)
			}
		})
	}
}

func attrOf(attrs []attribute.KeyValue, key string) string {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.String()
		}
	}
	return ""
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
