package pulse

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/trace"
	"github.com/WalletConnect/walletconnect-go/tracefile"
)

// server records the batches posted to it and answers with the next status
// in codes, then 200.
type server struct {
	*httptest.Server
	mu      sync.Mutex
	codes   []int
	queries []url.Values
	origins []string
	batches [][]Event
	bodies  []string
}

func newServer(t *testing.T, codes ...int) *server {
	s := &server{codes: codes}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		code := http.StatusOK
		if len(s.codes) > 0 {
			code, s.codes = s.codes[0], s.codes[1:]
		}
		if code == http.StatusOK {
			var b []Event
			if err := json.Unmarshal(body, &b); err != nil {
				t.Errorf("body is not an event array: %v", err)
			}
			s.queries = append(s.queries, r.URL.Query())
			s.origins = append(s.origins, r.Header.Get("Origin"))
			s.batches = append(s.batches, b)
			s.bodies = append(s.bodies, string(body))
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Event
	for _, b := range s.batches {
		out = append(out, b...)
	}
	return out
}

func newReporter(t *testing.T, s *server, o Options) *Reporter {
	o.URL, o.ProjectID, o.Version = s.URL, "project", "1.2.3"
	if o.Domain == "" {
		o.Domain = "https://app.example"
	}
	if o.Interval == 0 {
		o.Interval = time.Hour
	}
	r, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

var t0 = time.Unix(1_800_000_000, 0)

func span(name string, attrs map[string]any, steps ...string) *tracefile.Span {
	s := &tracefile.Span{Name: name, Start: t0, End: t0.Add(1500 * time.Millisecond), Status: tracefile.Status{Code: tracefile.StatusOK}}
	for k, v := range attrs {
		s.Attributes = append(s.Attributes, tracefile.Attribute{Key: k, Value: v})
	}
	for _, step := range steps {
		s.Events = append(s.Events, tracefile.Event{Name: step, Time: t0})
	}
	return s
}

func failed(s *tracefile.Span, errType string) *tracefile.Span {
	s.Status = tracefile.Status{Code: tracefile.StatusError, Message: "pair: wc:abc@2?symKey=secret"}
	s.Attributes = append(s.Attributes, tracefile.Attribute{Key: trace.AttrErrorType, Value: errType})
	return s
}

func TestReportsAPIActionsOnly(t *testing.T) {
	s := newServer(t)
	r := newReporter(t, s, Options{Domain: "https://dapp.example/connect?x=1"})
	r.Export(span("walletconnect.request", map[string]any{trace.AttrTopic: "t1", trace.AttrChainID: "eip155:1", trace.AttrMethod: "personal_sign"},
		"request published"))
	r.Export(span("relay.incoming", map[string]any{trace.AttrTopic: "t1"}))
	r.Export(span("irn_publish", nil))
	r.Export(span("wc.peer.answer", nil))
	r.Export(span("walletconnect.expiry_sweep", nil))
	r.Export(failed(span("walletconnect.approve", map[string]any{trace.AttrSessionTopic: "t2", trace.AttrProposalID: int64(7)}), "rpc_5000"))
	r.Close()

	evs := s.events()
	if len(evs) != 2 {
		t.Fatalf("sent %d events, want the 2 API actions: %+v", len(evs), evs)
	}
	ok, bad := evs[0], evs[1]
	want := Props{Event: "TRACE", Type: "request", Properties: Properties{Topic: "t1", Trace: []string{"request_published"}, Action: "request", DurationMs: 1500}}
	if ok.BundleID != "project" || ok.Domain != "https://dapp.example/connect?x=1" || ok.Timestamp != t0.Add(1500*time.Millisecond).UnixMilli() ||
		len(ok.EventID) != 36 || ok.EventID[14] != '4' || ok.Props.Event != want.Event || ok.Props.Type != want.Type ||
		ok.Props.Properties.Topic != "t1" || len(ok.Props.Properties.Trace) != 1 || ok.Props.Properties.Trace[0] != "request_published" ||
		ok.Props.Properties.DurationMs != 1500 {
		t.Fatalf("success event %+v, want %+v", ok, want)
	}
	if bad.Props.Event != "ERROR" || bad.Props.Type != "rpc_5000" || bad.Props.Properties.Action != "approve" || bad.Props.Properties.Topic != "t2" {
		t.Fatalf("error event %+v", bad)
	}
	q := s.queries[0]
	if q.Get("projectId") != "project" || q.Get("st") != "events_sdk" || q.Get("sv") != "go-1.2.3" {
		t.Fatalf("query %v", q)
	}
	if s.origins[0] != "https://dapp.example" {
		t.Fatalf("Origin %q, want the app URL's origin", s.origins[0])
	}
}

// timed builds a span from steps at their offsets in milliseconds. Relay
// steps come in send, reply pairs, and each pair shares a relay id.
func timed(name string, steps ...any) *tracefile.Span {
	s := span(name, nil)
	for i := 0; i < len(steps); i += 2 {
		e := tracefile.Event{Name: steps[i].(string), Time: t0.Add(time.Duration(steps[i+1].(int)) * time.Millisecond)}
		if strings.HasPrefix(e.Name, "relay ") {
			e.Attributes = []tracefile.Attribute{{Key: trace.AttrRelayID, Value: int64(i/4 + 1)}}
		}
		s.Events = append(s.Events, e)
	}
	return s
}

// relay_ms, relay_connect_ms and peer_ms split an action's duration, so a
// slow relay or a slow connection is not hidden behind a user taking minutes
// to approve.
func TestSplitsRelayAndPeerTime(t *testing.T) {
	s := newServer(t)
	r := newReporter(t, s, Options{})
	r.Export(timed("walletconnect.request", "relay send", 1, "relay reply", 26, "request published", 26, "peer answered", 2151))
	connect := timed("walletconnect.connect", "relay send", 130, "relay reply", 170, "relay send", 171, "relay reply", 230, "proposal published", 230)
	waited := tracefile.Event{Name: "relay waited", Time: t0.Add(130 * time.Millisecond),
		Attributes: []tracefile.Attribute{{Key: trace.AttrRelayWaitedMs, Value: int64(128)}}}
	connect.Events = append([]tracefile.Event{waited}, connect.Events...)
	r.Export(connect)
	r.Export(failed(span("walletconnect.pair", nil), "expired"))
	r.Close()

	evs := s.events()
	ms := func(p *int64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	for i, want := range [][3]any{{int64(25), nil, int64(2125)}, {int64(99), int64(128), nil}, {nil, nil, nil}} {
		p := evs[i].Props.Properties
		if got := [3]any{ms(p.RelayMs), ms(p.RelayConnectMs), ms(p.PeerMs)}; got != want {
			t.Errorf("%s: relay_ms, relay_connect_ms, peer_ms = %v, want %v", p.Action, got, want)
		}
	}
	if strings.Count(s.bodies[0], `"relay_ms"`) != 2 || strings.Count(s.bodies[0], `"relay_connect_ms"`) != 1 || strings.Count(s.bodies[0], `"peer_ms"`) != 1 {
		t.Fatalf("absent times must be left out, not sent as 0: %s", s.bodies[0])
	}
}

// Only the fixed fields leave the client: never other attributes or the
// error message, which can quote a pairing URI.
func TestSendsNothingElse(t *testing.T) {
	s := newServer(t)
	r := newReporter(t, s, Options{})
	r.Export(failed(span("walletconnect.pair", map[string]any{"wc.uri": "wc:abc@2?symKey=secret", trace.AttrChainID: "eip155:1"}), "expired"))
	r.Close()
	if len(s.bodies) != 1 {
		t.Fatalf("%d batches, want 1", len(s.bodies))
	}
	for _, leak := range []string{"secret", "symKey", "eip155"} {
		if strings.Contains(s.bodies[0], leak) {
			t.Errorf("body carries %q: %s", leak, s.bodies[0])
		}
	}
}

func TestBatchesBySize(t *testing.T) {
	s := newServer(t)
	r := newReporter(t, s, Options{BatchSize: 3})
	for range 3 {
		r.Export(span("walletconnect.ping", nil))
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(s.events()) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := len(s.events()); n != 3 {
		t.Fatalf("a full batch sent %d events, want 3", n)
	}
	r.Export(span("walletconnect.ping", nil))
	time.Sleep(50 * time.Millisecond)
	if n := len(s.events()); n != 3 {
		t.Fatalf("a partial batch was sent before the interval: %d events", n)
	}
	r.Close()
	if n := len(s.events()); n != 4 {
		t.Fatalf("sent %d events after Close, want 4", n)
	}
}

func TestSendsOnInterval(t *testing.T) {
	s := newServer(t)
	r := newReporter(t, s, Options{Interval: 20 * time.Millisecond})
	r.Export(span("walletconnect.ping", nil))
	deadline := time.Now().Add(2 * time.Second)
	for len(s.events()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(s.events()) != 1 {
		t.Fatal("event not sent on the interval")
	}
}

func TestRetriesUnavailableAndDropsRefused(t *testing.T) {
	s := newServer(t, http.StatusServiceUnavailable, http.StatusBadRequest)
	r := newReporter(t, s, Options{})
	r.Export(span("walletconnect.ping", nil))
	r.flush(t.Context()) // 503: kept
	r.Export(span("walletconnect.extend", nil))
	r.flush(t.Context()) // 400: both dropped
	r.Export(span("walletconnect.disconnect", nil))
	r.Close()
	evs := s.events()
	if len(evs) != 1 || evs[0].Props.Type != "disconnect" {
		t.Fatalf("sent %+v, want only the event after the refused batch", evs)
	}
}

func TestBufferKeepsNewest(t *testing.T) {
	s := newServer(t)
	r := newReporter(t, s, Options{MaxBuffer: 2, BatchSize: 10})
	for _, name := range []string{"ping", "extend", "update"} {
		r.Export(span("walletconnect."+name, nil))
	}
	r.Close()
	evs := s.events()
	if len(evs) != 2 || evs[0].Props.Type != "extend" || evs[1].Props.Type != "update" {
		t.Fatalf("sent %+v, want the 2 newest", evs)
	}
}

func TestNeedsAnOrigin(t *testing.T) {
	for _, domain := range []string{"", "example.com", "not a url"} {
		if _, err := New(Options{Domain: domain}); !errors.Is(err, ErrNoOrigin) {
			t.Errorf("%q: got %v, want ErrNoOrigin", domain, err)
		}
	}
}

func TestCloseGivesUpOnUnreachableServer(t *testing.T) {
	r, err := New(Options{URL: "http://127.0.0.1:1", Domain: "https://app.example", Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	r.Export(span("walletconnect.ping", nil))
	start := time.Now()
	r.Close()
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Close took %v", d)
	}
}
