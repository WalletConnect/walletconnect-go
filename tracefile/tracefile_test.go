package tracefile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

var t0 = time.Date(2026, 10, 23, 9, 0, 0, 123456789, time.UTC)

func id(n int, width int) string { return fmt.Sprintf("%0*x", width, n) }

func mk(n int, name string, d time.Duration, attrs ...Attribute) *Span {
	return &Span{TraceID: id(n, 32), SpanID: id(n, 16), Name: name, Kind: KindInternal,
		Start: t0.Add(time.Duration(n) * time.Second), End: t0.Add(time.Duration(n)*time.Second + d),
		Attributes: attrs, Status: Status{Code: StatusOK}}
}

func full() *Span {
	s := mk(1, "wc.peer.answer", 1500*time.Millisecond,
		Attribute{"wc.topic", "abc"}, Attribute{"wc.rpc.id", int64(1792574820228847)},
		Attribute{"wc.acked", true}, Attribute{"relay.took", 0.076})
	s.Kind = KindClient
	s.ParentSpanID = id(2, 16)
	s.Events = []Event{{Name: "sent", Time: t0.Add(time.Second + 4500*time.Microsecond), Attributes: []Attribute{{"relay.socket", int64(3)}}}}
	s.Links = []Link{{TraceID: id(7, 32), SpanID: id(8, 16)}}
	s.Status = Status{Code: StatusError, Message: "request expired"}
	return s
}

func readAll(t *testing.T, paths ...string) []*Span {
	t.Helper()
	var out []*Span
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for s, err := range Read(bytes.NewReader(data)) {
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			out = append(out, s)
		}
	}
	return out
}

func TestWriteAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wc.trace.jsonl")
	w, err := Open(path, Options{Service: "test-wallet", Version: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	want := []*Span{full(), mk(3, "walletconnect.ping", 20*time.Millisecond)}
	for _, s := range want {
		if err := w.Export(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Export(want[1]); err == nil {
		t.Fatal("export after close should fail")
	}
	got := readAll(t, path)
	if len(got) != len(want) {
		t.Fatalf("read %d spans, want %d", len(got), len(want))
	}
	for i := range want {
		if fmt.Sprint(got[i]) != fmt.Sprint(want[i]) {
			t.Errorf("span %d\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
	// Appending to an existing file keeps what was there.
	w, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Export(mk(4, "walletconnect.ping", time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if got := readAll(t, path); len(got) != 3 {
		t.Fatalf("after reopen read %d spans, want 3", len(got))
	}
}

// A line must be what the collector's OTLP JSON receivers accept: this
// decodes one with the collector's own unmarshaler, which checks the hex
// ids, string-encoded integers and integer enums.
func TestCollectorDecodesLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wc.trace.jsonl")
	w, err := Open(path, Options{Service: "test-wallet", Version: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Export(full()); err != nil {
		t.Fatal(err)
	}
	w.Close()
	line, _ := os.ReadFile(path)
	if n := bytes.Count(line, []byte("\n")); n != 1 || line[len(line)-1] != '\n' {
		t.Fatalf("one span should be one line, got %d newlines", n)
	}
	td, err := (&ptrace.JSONUnmarshaler{}).UnmarshalTraces(bytes.TrimSpace(line))
	if err != nil {
		t.Fatalf("collector cannot decode the line: %v\n%s", err, line)
	}
	rs := td.ResourceSpans().At(0)
	if v, _ := rs.Resource().Attributes().Get("service.name"); v.Str() != "test-wallet" {
		t.Errorf("service.name = %q", v.Str())
	}
	ss := rs.ScopeSpans().At(0)
	if ss.Scope().Name() != scopeName || ss.Scope().Version() != "1.2.3" {
		t.Errorf("scope = %s %s", ss.Scope().Name(), ss.Scope().Version())
	}
	sp := ss.Spans().At(0)
	want := full()
	if got := sp.TraceID().String(); got != want.TraceID {
		t.Errorf("trace id %s, want %s", got, want.TraceID)
	}
	if got := sp.SpanID().String(); got != want.SpanID {
		t.Errorf("span id %s, want %s", got, want.SpanID)
	}
	if got := sp.ParentSpanID().String(); got != want.ParentSpanID {
		t.Errorf("parent span id %s, want %s", got, want.ParentSpanID)
	}
	if sp.Name() != want.Name || sp.Kind() != ptrace.SpanKindClient {
		t.Errorf("name %q kind %v", sp.Name(), sp.Kind())
	}
	if !sp.StartTimestamp().AsTime().Equal(want.Start) || !sp.EndTimestamp().AsTime().Equal(want.End) {
		t.Errorf("times %v %v, want %v %v", sp.StartTimestamp().AsTime(), sp.EndTimestamp().AsTime(), want.Start, want.End)
	}
	if sp.Status().Code() != ptrace.StatusCodeError || sp.Status().Message() != "request expired" {
		t.Errorf("status %v %q", sp.Status().Code(), sp.Status().Message())
	}
	attrs := sp.Attributes()
	if v, _ := attrs.Get("wc.topic"); v.Type() != pcommon.ValueTypeStr || v.Str() != "abc" {
		t.Errorf("wc.topic = %v", v)
	}
	if v, _ := attrs.Get("wc.rpc.id"); v.Type() != pcommon.ValueTypeInt || v.Int() != 1792574820228847 {
		t.Errorf("wc.rpc.id = %v", v)
	}
	if v, _ := attrs.Get("wc.acked"); v.Type() != pcommon.ValueTypeBool || !v.Bool() {
		t.Errorf("wc.acked = %v", v)
	}
	if v, _ := attrs.Get("relay.took"); v.Type() != pcommon.ValueTypeDouble || v.Double() != 0.076 {
		t.Errorf("relay.took = %v", v)
	}
	if ev := sp.Events(); ev.Len() != 1 || ev.At(0).Name() != "sent" || !ev.At(0).Timestamp().AsTime().Equal(want.Events[0].Time) {
		t.Errorf("events = %v", ev)
	} else if v, _ := ev.At(0).Attributes().Get("relay.socket"); v.Int() != 3 {
		t.Errorf("event attribute relay.socket = %v", v)
	}
	if ln := sp.Links(); ln.Len() != 1 || ln.At(0).TraceID().String() != want.Links[0].TraceID ||
		ln.At(0).SpanID().String() != want.Links[0].SpanID {
		t.Errorf("links = %v", ln)
	}
}

func TestDecodeAcceptsNamesAndNumbers(t *testing.T) {
	line := `{"resourceSpans":[{"resource":{},"scopeSpans":[{"scope":{"name":"x"},"spans":[{"traceId":"` + id(1, 32) + `","spanId":"` + id(1, 16) +
		`","name":"n","kind":"SPAN_KIND_CONSUMER","startTimeUnixNano":1000,"endTimeUnixNano":"2000","attributes":[{"key":"k","value":{"intValue":5}}],"status":{"code":"STATUS_CODE_OK"}}]}]}]}`
	spans, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	s := spans[0]
	if s.Kind != KindConsumer || s.Status.Code != StatusOK || s.Start.UnixNano() != 1000 || s.End.UnixNano() != 2000 || s.Attributes[0].Value != int64(5) {
		t.Errorf("decoded %+v", s)
	}
}

func TestBadIDsRejected(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "t.jsonl"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Export(&Span{TraceID: "abc", SpanID: id(1, 16), Name: "x"}); err == nil {
		t.Fatal("short trace id should be rejected")
	}
}

func TestRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wc.trace.jsonl")
	const limit = 3 * 1500
	w, err := Open(path, Options{Limit: limit})
	if err != nil {
		t.Fatal(err)
	}
	const n = 40
	for i := range n {
		if err := w.Export(mk(i, fmt.Sprintf("s%02d", i), time.Millisecond, Attribute{"wc.topic", strings.Repeat("t", 64)})); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	files := Files(path)
	if len(files) != keep {
		t.Fatalf("files = %v, want %d", files, keep)
	}
	var total int64
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if st.Size() > limit/keep {
			t.Errorf("%s is %d bytes, over the per-file limit %d", f, st.Size(), limit/keep)
		}
		total += st.Size()
	}
	if total > limit {
		t.Errorf("%d bytes on disk, over the limit %d", total, limit)
	}
	// Oldest first, the retained spans are a contiguous run ending with the last one.
	spans := readAll(t, files...)
	if len(spans) == 0 || len(spans) >= n {
		t.Fatalf("retained %d of %d spans", len(spans), n)
	}
	first := n - len(spans)
	for i, s := range spans {
		if want := fmt.Sprintf("s%02d", first+i); s.Name != want {
			t.Fatalf("span %d is %s, want %s", i, s.Name, want)
		}
	}
	if spans[len(spans)-1].Name != fmt.Sprintf("s%02d", n-1) {
		t.Fatalf("last span is %s", spans[len(spans)-1].Name)
	}
}

func TestReadSkipsBadLines(t *testing.T) {
	var buf bytes.Buffer
	w, _ := Open(filepath.Join(t.TempDir(), "t.jsonl"), Options{})
	line1, _ := encode(mk(1, "a", time.Millisecond))
	line2, _ := encode(mk(2, "b", time.Millisecond))
	buf.Write(w.prefix)
	buf.Write(line1)
	buf.Write(w.suffix)
	buf.WriteString("garbage\n")
	buf.Write(w.prefix)
	buf.Write(line2)
	buf.Write(w.suffix)
	w.Close()
	var names []string
	var errs int
	for s, err := range Read(&buf) {
		if err != nil {
			errs++
			continue
		}
		names = append(names, s.Name)
	}
	if errs != 1 || fmt.Sprint(names) != "[a b]" {
		t.Fatalf("errs %d names %v", errs, names)
	}
}

func TestSummarize(t *testing.T) {
	var buf bytes.Buffer
	w, _ := Open(filepath.Join(t.TempDir(), "t.jsonl"), Options{})
	defer w.Close()
	write := func(s *Span) {
		line, err := encode(s)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(w.prefix)
		buf.Write(line)
		buf.Write(w.suffix)
	}
	n := 0
	next := func(name string, d time.Duration, attrs ...Attribute) *Span { n++; return mk(n, name, d, attrs...) }
	for i, d := range []time.Duration{100, 200, 300, 400, 5000} {
		s := next("walletconnect.request", d*time.Millisecond, Attribute{"wc.method", "personal_sign"})
		if i == 4 {
			s.Status = Status{Code: StatusError, Message: "timeout"}
		}
		write(s)
	}
	write(next("walletconnect.request", 50*time.Millisecond, Attribute{"wc.method", "eth_sendTransaction"}))
	write(next("wc.peer.answer", time.Second, Attribute{"rpc.method", "wc_sessionRequest"}, Attribute{"wc.method", "personal_sign"}, Attribute{"wc.outcome", "ok"}))
	write(next("wc.peer.answer", 5*time.Second, Attribute{"rpc.method", "wc_sessionRequest"}, Attribute{"wc.method", "personal_sign"}, Attribute{"wc.outcome", "timeout"}))
	write(next("relay.publish", 80*time.Millisecond, Attribute{"rpc.method", "irn_publish"}, Attribute{"wc.outcome", "ok"}))
	write(next("relay.reconnect", 0, Attribute{"wc.reason", "connection_lost"}))
	write(next("relay.reconnect", 0, Attribute{"wc.reason", "connection_lost"}))
	in := next("relay.incoming", 2*time.Millisecond)
	in.Events = []Event{{Name: "dropped", Time: in.Start, Attributes: []Attribute{{"wc.reason", "unknown topic"}}}}
	write(in)
	buf.WriteString("not json\n")

	sum, err := Summarize(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Spans != 12 || sum.Errors != 1 {
		t.Fatalf("spans %d errors %d", sum.Spans, sum.Errors)
	}
	if !sum.From.Equal(t0.Add(time.Second)) || !sum.To.Equal(t0.Add(13*time.Second)) {
		t.Errorf("from %v to %v", sum.From, sum.To)
	}
	find := func(name string) *Stat {
		for _, st := range sum.Stats {
			if st.Name == name {
				return st
			}
		}
		t.Fatalf("no stat %s", name)
		return nil
	}
	req := find("walletconnect.request")
	if req.Count != 6 || req.OK != 5 || req.Failed != 1 || req.P50 != 200*time.Millisecond || req.P95 != 5*time.Second || req.Max != 5*time.Second {
		t.Errorf("request: %+v", req)
	}
	if len(req.Methods) != 2 || req.Methods[0].Method != "eth_sendTransaction" || req.Methods[1].Method != "personal_sign" || req.Methods[1].Count != 5 || req.Methods[1].P50 != 300*time.Millisecond {
		t.Errorf("request methods: %+v %+v", req.Methods[0], req.Methods[1])
	}
	ans := find("wc.peer.answer")
	if ans.Outcomes["ok"] != 1 || ans.Outcomes["timeout"] != 1 || len(ans.Methods) != 1 || ans.Methods[0].Method != "wc_sessionRequest/personal_sign" {
		t.Errorf("answer: %+v", ans)
	}
	if pub := find("relay.publish"); len(pub.Methods) != 0 || pub.Outcomes["ok"] != 1 {
		t.Errorf("publish: %+v", pub)
	}
	if sum.Reconnects["connection_lost"] != 2 || sum.Dropped["unknown topic"] != 1 {
		t.Errorf("reconnects %v dropped %v", sum.Reconnects, sum.Dropped)
	}
	out := sum.Format()
	for _, want := range []string{"12 spans from 2026-10-23T09:00:01Z to 2026-10-23T09:00:13Z, 1 unreadable lines",
		"walletconnect.request", "  personal_sign", "  wc_sessionRequest/personal_sign", "ok 1, timeout 1",
		"reconnects: connection_lost 2", "dropped: unknown topic 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("formatted summary lacks %q:\n%s", want, out)
		}
	}
	t.Log("\n" + out)
}

func TestSummarizeFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wc.trace.jsonl")
	w, err := Open(path, Options{Limit: 3 * 1200})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 30 {
		if err := w.Export(mk(i, "walletconnect.ping", time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	sum, err := SummarizeFiles(Files(path)...)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Spans == 0 || sum.Spans >= 30 || sum.Stats[0].Count != sum.Spans {
		t.Fatalf("summarized %d spans across %v", sum.Spans, Files(path))
	}
}
