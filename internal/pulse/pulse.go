// Package pulse reports API actions to WalletConnect's Pulse API, the
// endpoint the JS SDK's event client posts to. It is a trace exporter: each
// API call's span becomes one event, "TRACE" when it succeeded and "ERROR"
// when it failed, carrying the action, its topic, the names of its steps and
// its duration. Nothing else leaves the client: no params, payloads, keys,
// addresses or error messages, only the error's fixed type.
//
// Events are sent in batches, every Interval or once BatchSize are waiting,
// and once more on Close. Sending never blocks the client; a failed batch
// is retried with the next one, and the buffer drops the oldest events past
// MaxBuffer.
package pulse

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/trace"
	"github.com/WalletConnect/walletconnect-go/tracefile"
)

// DefaultURL is the Pulse batch endpoint.
const DefaultURL = "https://pulse.walletconnect.org/batch"

type Options struct {
	URL       string // default DefaultURL
	ProjectID string
	// Domain is the app's metadata URL. Its origin is sent as the Origin
	// header, which Pulse checks against the project's allowed domains.
	Domain    string
	Version   string
	Logger    *slog.Logger
	Client    *http.Client  // default: a client with a 10 s timeout
	Interval  time.Duration // default 30 s
	BatchSize int           // default 100
	MaxBuffer int           // default 1000
}

// actions are the API calls reported, by span name.
var actions = map[string]string{
	"walletconnect.connect":       "connect",
	"walletconnect.request":       "request",
	"walletconnect.pair":          "pair",
	"walletconnect.approve":       "approve",
	"walletconnect.reject":        "reject",
	"walletconnect.respond":       "respond",
	"walletconnect.respond_error": "respond_error",
	"walletconnect.update":        "update",
	"walletconnect.emit":          "emit",
	"walletconnect.extend":        "extend",
	"walletconnect.ping":          "ping",
	"walletconnect.disconnect":    "disconnect",
}

type Event struct {
	EventID   string `json:"eventId"`
	BundleID  string `json:"bundleId"`
	Timestamp int64  `json:"timestamp"`
	Domain    string `json:"domain"`
	Props     Props  `json:"props"`
}

type Props struct {
	Event      string     `json:"event"`
	Type       string     `json:"type"`
	Properties Properties `json:"properties"`
}

type Properties struct {
	Topic      string   `json:"topic"`
	Trace      []string `json:"trace"`
	Action     string   `json:"action"`
	DurationMs int64    `json:"duration_ms"`
	// RelayMs is the time spent on relay round trips, from each send to its
	// reply; nil when the action made no relay call.
	RelayMs *int64 `json:"relay_ms,omitempty"`
	// PeerMs is the wait for the peer's answer, from the last relay reply
	// before it; nil when the action does not wait for the peer.
	PeerMs *int64 `json:"peer_ms,omitempty"`
}

type Reporter struct {
	o      Options
	url    string
	origin string
	wake   chan struct{}
	stop   chan struct{}
	done   chan struct{}
	closed sync.Once

	mu      sync.Mutex
	pending []Event

	warned sync.Once
}

// ErrNoOrigin means Options.Domain gives no origin to send. Pulse refuses
// requests without one.
var ErrNoOrigin = errors.New("pulse: an app URL with a scheme and host is required")

// New starts a reporter. Close flushes it and stops it.
func New(o Options) (*Reporter, error) {
	origin := originOf(o.Domain)
	if origin == "" {
		return nil, ErrNoOrigin
	}
	if o.URL == "" {
		o.URL = DefaultURL
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if o.Interval <= 0 {
		o.Interval = 30 * time.Second
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 100
	}
	if o.MaxBuffer <= 0 {
		o.MaxBuffer = 1000
	}
	q := url.Values{"projectId": {o.ProjectID}, "st": {"events_sdk"}, "sv": {"go-" + o.Version}}
	r := &Reporter{o: o, url: o.URL + "?" + q.Encode(), origin: origin, wake: make(chan struct{}, 1),
		stop: make(chan struct{}), done: make(chan struct{})}
	go r.run()
	return r, nil
}

// originOf is the scheme and host of an app URL, as a browser sends it.
func originOf(appURL string) string {
	u, err := url.Parse(appURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Export queues an event for an API action span and ignores other spans.
func (r *Reporter) Export(s *tracefile.Span) error {
	action, ok := actions[s.Name]
	if !ok {
		return nil
	}
	ev := Event{EventID: uuid(), BundleID: r.o.ProjectID, Timestamp: s.End.UnixMilli(), Domain: r.o.Domain,
		Props: Props{Event: "TRACE", Type: action, Properties: Properties{
			Topic: topic(s), Trace: steps(s), Action: action, DurationMs: s.Duration().Milliseconds(),
			RelayMs: relayMs(s), PeerMs: peerMs(s)}}}
	if s.Status.Code == tracefile.StatusError {
		ev.Props.Event = "ERROR"
		ev.Props.Type = s.Attr(trace.AttrErrorType)
		if ev.Props.Type == "" {
			ev.Props.Type = "other"
		}
	}
	r.mu.Lock()
	r.pending = append(r.pending, ev)
	if over := len(r.pending) - r.o.MaxBuffer; over > 0 {
		r.pending = r.pending[over:]
	}
	full := len(r.pending) >= r.o.BatchSize
	r.mu.Unlock()
	if full {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

// Close sends what is waiting, giving up after a few seconds, and stops
// the reporter.
func (r *Reporter) Close() {
	r.closed.Do(func() {
		close(r.stop)
		<-r.done
	})
}

func (r *Reporter) run() {
	defer close(r.done)
	tick := time.NewTicker(r.o.Interval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
		case <-r.wake:
		case <-r.stop:
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			r.flush(ctx)
			cancel()
			return
		}
		r.flush(context.Background())
	}
}

// flush sends the waiting events in batches. A batch the server refuses is
// dropped; one that could not be delivered goes back to wait for the next
// flush.
func (r *Reporter) flush(ctx context.Context) {
	for {
		r.mu.Lock()
		n := min(len(r.pending), r.o.BatchSize)
		batch := r.pending[:n:n]
		r.pending = r.pending[n:]
		r.mu.Unlock()
		if n == 0 {
			return
		}
		retry, err := r.send(ctx, batch)
		if err == nil {
			continue
		}
		r.warned.Do(func() {
			if r.o.Logger != nil {
				r.o.Logger.Warn("pulse: sending telemetry failed; later failures are not logged", "err", err)
			}
		})
		if retry {
			r.mu.Lock()
			r.pending = append(batch, r.pending...)
			if over := len(r.pending) - r.o.MaxBuffer; over > 0 {
				r.pending = r.pending[over:]
			}
			r.mu.Unlock()
		}
		return
	}
}

func (r *Reporter) send(ctx context.Context, batch []Event) (retry bool, err error) {
	body, err := json.Marshal(batch)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.Header.Set("Origin", r.origin)
	res, err := r.o.Client.Do(req)
	if err != nil {
		return true, err
	}
	res.Body.Close()
	switch {
	case res.StatusCode < 300:
		return false, nil
	case res.StatusCode >= 500 || res.StatusCode == http.StatusTooManyRequests:
		return true, fmt.Errorf("pulse: %s", res.Status)
	default:
		return false, fmt.Errorf("pulse: %s", res.Status)
	}
}

// topic is the span's session or pairing topic, the identifier JS reports.
func topic(s *tracefile.Span) string {
	for _, key := range []string{trace.AttrTopic, trace.AttrSessionTopic, trace.AttrPairingTopic} {
		if v := s.Attr(key); v != "" {
			return v
		}
	}
	return ""
}

// steps are the span's event names in snake case, like JS trace entries.
func steps(s *tracefile.Span) []string {
	out := make([]string, 0, len(s.Events))
	for _, e := range s.Events {
		out = append(out, strings.ReplaceAll(e.Name, " ", "_"))
	}
	return out
}

func relayMs(s *tracefile.Span) *int64 {
	sent := map[string]time.Time{}
	var total time.Duration
	replied := false
	for _, e := range s.Events {
		id := e.Attr(trace.AttrRelayID)
		switch e.Name {
		case "relay send":
			sent[id] = e.Time
		case "relay reply":
			if t, ok := sent[id]; ok {
				total += e.Time.Sub(t)
				replied = true
			}
		}
	}
	if !replied {
		return nil
	}
	ms := total.Milliseconds()
	return &ms
}

func peerMs(s *tracefile.Span) *int64 {
	var published time.Time
	for _, e := range s.Events {
		switch e.Name {
		case "relay reply":
			published = e.Time
		case "peer answered":
			if published.IsZero() {
				return nil
			}
			ms := e.Time.Sub(published).Milliseconds()
			return &ms
		}
	}
	return nil
}

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
