package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/testenv"
	"github.com/WalletConnect/walletconnect-go/internal/trace"
	"github.com/WalletConnect/walletconnect-go/internal/types"
	"github.com/WalletConnect/walletconnect-go/tracefile"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestCallSpanNames(t *testing.T) {
	for method, want := range map[string]string{"irn_publish": "relay.publish", "irn_subscribe": "relay.subscribe",
		"irn_batchSubscribe": "relay.batch_subscribe", "irn_unsubscribe": "relay.unsubscribe"} {
		if got := (&call{method: method}).spanName(); got != want {
			t.Errorf("%s -> %s, want %s", method, got, want)
		}
	}
}

// finish ends a call's span with the outcome and records the call latency.
func TestFinishEndsCallSpan(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()); _ = mp.Shutdown(context.Background()) })
	tr := trace.New(trace.Options{TracerProvider: tp, MeterProvider: mp})
	r := &runner{c: &Client{cfg: Config{Tracer: tr}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}}

	ctx := context.Background()
	ok := &call{ctx: ctx, method: "irn_publish", id: 9, firstSent: time.Now().Add(-80 * time.Millisecond), reply: make(chan result, 1)}
	ok.span, _ = tr.Child(ctx, ok.spanName(), "relayId", ok.id, "topic", "abc")
	r.finish(ok, json.RawMessage("true"), nil)
	timedOut := &call{ctx: ctx, method: "irn_subscribe", id: 10, firstSent: time.Now().Add(-time.Second), reply: make(chan result, 1)}
	timedOut.span, _ = tr.Child(ctx, timedOut.spanName(), "relayId", timedOut.id)
	r.finish(timedOut, nil, types.ErrTimeout)
	never := &call{ctx: ctx, method: "irn_publish", reply: make(chan result, 1)} // never written: no span, no metric
	r.finish(never, nil, types.ErrClosed)

	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2: %+v", len(spans), spans)
	}
	byName := map[string]tracetest.SpanStub{}
	for _, s := range spans {
		byName[s.Name] = s
	}
	if s := byName["relay.publish"]; s.Status.Code != codes.Ok || attrOf(s, trace.AttrOutcome) != "ok" || attrOf(s, trace.AttrRelayID) != "9" || attrOf(s, trace.AttrTopic) != "abc" {
		t.Fatalf("publish span %+v", s)
	}
	if s := byName["relay.subscribe"]; s.Status.Code != codes.Error || attrOf(s, trace.AttrOutcome) != "timeout" {
		t.Fatalf("subscribe span %+v", s)
	}
	if ok.span != nil || timedOut.span != nil {
		t.Fatal("finish should drop the span reference")
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "walletconnect.relay.call.duration" {
				if h := m.Data.(metricdata.Histogram[float64]); len(h.DataPoints) != 2 {
					t.Fatalf("relay.call.duration points %+v, want one per outcome", h.DataPoints)
				}
				return
			}
		}
	}
	t.Fatal("walletconnect.relay.call.duration not recorded")
}

func attrOf(s tracetest.SpanStub, key string) string {
	for _, a := range s.Attributes {
		if string(a.Key) == key {
			return a.Value.String()
		}
	}
	return ""
}

type spanSink struct {
	mu    sync.Mutex
	spans []*tracefile.Span
}

func (s *spanSink) Export(sp *tracefile.Span) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans = append(s.spans, sp)
	return nil
}

// A call made before the socket is up records how long it waited for it on
// its first relay send; a call on a live socket records no wait.
func TestLiveSendRecordsConnectionWait(t *testing.T) {
	url, project := testenv.Relay(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sink := &spanSink{}
	tr := trace.New(trace.Options{Exporter: sink})
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	c := New(Config{URL: url, ProjectID: project, UserAgent: "wc-2/go-test/test/test", Key: priv, Tracer: tr,
		Topics: func(context.Context) ([]string, error) { return nil, nil }, Deliver: func(Incoming) {}, Logger: testLogger()})
	go c.Run(ctx)

	for range 2 {
		span, sctx := tr.Start(ctx, "request")
		publish(t, sctx, c, randomTopic(t), randomMessage(t))
		span.End(nil)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	var waits []string
	for _, s := range sink.spans {
		if s.Name != "walletconnect.request" {
			continue
		}
		for _, e := range s.Events {
			if e.Name == "relay send" {
				waits = append(waits, e.Attr(trace.AttrRelayWaitedMs))
			}
		}
	}
	if len(waits) != 2 || waits[0] == "" || waits[1] != "" {
		t.Fatalf("waits %q, want one on the first send only", waits)
	}
}
