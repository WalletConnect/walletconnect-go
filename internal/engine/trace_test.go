package engine

import (
	"context"
	"encoding/json"
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
