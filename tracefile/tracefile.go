// Package tracefile writes and reads the SDK's trace file: one span per line,
// each line an OTLP JSON export request, so the file can be read with jq or
// replayed into any OpenTelemetry backend through a collector.
//
// The client writes the file on its own when its storage is a file, so an
// integrator has a trace of every request without configuring anything; see
// walletconnect.Options.TraceFile. Summarize turns a file into counts and
// latencies per span name and method.
package tracefile

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"strconv"
	"sync"
	"time"
)

// Span is one recorded span. Identifiers are lowercase hex, as OTLP JSON
// encodes them: 32 characters for a trace, 16 for a span.
type Span struct {
	TraceID      string
	SpanID       string
	ParentSpanID string // "" for a root span
	Name         string
	Kind         Kind
	Start, End   time.Time
	Attributes   []Attribute
	Events       []Event
	Links        []Link
	Status       Status
}

// Duration is the span's length.
func (s *Span) Duration() time.Duration { return s.End.Sub(s.Start) }

// Attr returns the span attribute with the key as a string, or "".
func (s *Span) Attr(key string) string { return attrString(s.Attributes, key) }

// Kind is the OTLP span kind.
type Kind int

const (
	KindUnspecified Kind = iota
	KindInternal
	KindServer
	KindClient
	KindProducer
	KindConsumer
)

// Attribute is a key with a string, bool, int64 or float64 value.
type Attribute struct {
	Key   string
	Value any
}

// Event is a named point in time within a span.
type Event struct {
	Name       string
	Time       time.Time
	Attributes []Attribute
}

// Attr returns the event attribute with the key as a string, or "".
func (e *Event) Attr(key string) string { return attrString(e.Attributes, key) }

// Link names another span this one belongs with, such as the incoming
// message that answered a request.
type Link struct {
	TraceID string
	SpanID  string
}

// Status is the span's outcome.
type Status struct {
	Code    StatusCode
	Message string
}

// StatusCode is the OTLP status code.
type StatusCode int

const (
	StatusUnset StatusCode = iota
	StatusOK
	StatusError
)

// Options configure a Writer.
type Options struct {
	// Limit is the most bytes kept on disk, across the current file and the
	// two rotated ones. Default 30 MB.
	Limit int64
	// Service is the resource's service.name; default "walletconnect-go".
	Service string
	// Version is the instrumentation scope version.
	Version string
}

const (
	DefaultLimit = 30 << 20
	keep         = 3 // files on disk: the current one and two rotated
	scopeName    = "github.com/WalletConnect/walletconnect-go"
)

// Writer appends spans to a file and rotates it by size. When the current
// file reaches a third of the limit it becomes <path>.1, the previous .1
// becomes .2, and the previous .2 is removed. Each span is written with one
// write call, so a crash loses nothing already written. Writer is safe for
// concurrent use.
type Writer struct {
	path   string
	max    int64 // bytes per file
	prefix []byte
	suffix []byte

	mu     sync.Mutex
	f      *os.File
	size   int64
	closed bool
}

// Open opens or creates the trace file at path, appending to it.
func Open(path string, o Options) (*Writer, error) {
	if o.Limit <= 0 {
		o.Limit = DefaultLimit
	}
	if o.Service == "" {
		o.Service = "walletconnect-go"
	}
	w := &Writer{path: path, max: o.Limit / keep}
	w.prefix, w.suffix = envelope(o.Service, o.Version)
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// envelope is the constant part of every line, around the span.
func envelope(service, version string) (prefix, suffix []byte) {
	res := keyValue{Key: "service.name", Value: anyValue{StringValue: &service}}
	rs, _ := json.Marshal(res)
	sc, _ := json.Marshal(scope{Name: scopeName, Version: version})
	prefix = fmt.Appendf(nil, `{"resourceSpans":[{"resource":{"attributes":[%s]},"scopeSpans":[{"scope":%s,"spans":[`, rs, sc)
	suffix = []byte("]}]}]}\n")
	return prefix, suffix
}

func (w *Writer) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, st.Size()
	return nil
}

// Path is the file being written.
func (w *Writer) Path() string { return w.path }

var errClosed = errors.New("tracefile: writer is closed")

// Export appends one span. It is the client's span exporter.
func (w *Writer) Export(s *Span) error {
	line, err := encode(s)
	if err != nil {
		return err
	}
	line = append(append(append(make([]byte, 0, len(w.prefix)+len(line)+len(w.suffix)), w.prefix...), line...), w.suffix...)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errClosed
	}
	if w.size > 0 && w.size+int64(len(line)) > w.max {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	n, err := w.f.Write(line)
	w.size += int64(n)
	return err
}

func (w *Writer) rotate() error {
	if err := w.f.Close(); err != nil {
		return err
	}
	last := fmt.Sprintf("%s.%d", w.path, keep-1)
	if err := os.Remove(last); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := keep - 1; i > 1; i-- {
		if err := os.Rename(fmt.Sprintf("%s.%d", w.path, i-1), fmt.Sprintf("%s.%d", w.path, i)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		return err
	}
	return w.open()
}

// Close closes the file. Later writes fail.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.f.Close()
}

// Files returns the paths that hold a trace file's history, oldest first:
// the rotated files that exist, then path itself.
func Files(path string) []string {
	var out []string
	for i := keep - 1; i >= 1; i-- {
		p := fmt.Sprintf("%s.%d", path, i)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return append(out, path)
}

// Read yields the spans in r, one line at a time, in file order. A line that
// does not parse yields an error and reading continues with the next line.
func Read(r io.Reader) iter.Seq2[*Span, error] {
	return func(yield func(*Span, error) bool) {
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				spans, derr := Decode(line)
				if derr != nil {
					if !yield(nil, derr) {
						return
					}
				}
				for _, s := range spans {
					if !yield(s, nil) {
						return
					}
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					yield(nil, err)
				}
				return
			}
		}
	}
}

// ---- OTLP JSON ----
//
// The wire shape follows the OTLP JSON encoding: 64-bit integers and
// timestamps as decimal strings, trace and span ids as hex strings, enums
// as integers. Decoding also accepts bare numbers and enum names.

type exportRequest struct {
	ResourceSpans []resourceSpans `json:"resourceSpans"`
}

type resourceSpans struct {
	Resource   resource     `json:"resource"`
	ScopeSpans []scopeSpans `json:"scopeSpans"`
}

type resource struct {
	Attributes []keyValue `json:"attributes,omitempty"`
}

type scopeSpans struct {
	Scope scope  `json:"scope"`
	Spans []span `json:"spans"`
}

type scope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type span struct {
	TraceID           string     `json:"traceId"`
	SpanID            string     `json:"spanId"`
	ParentSpanID      string     `json:"parentSpanId,omitempty"`
	Name              string     `json:"name"`
	Kind              jsonEnum   `json:"kind,omitempty"`
	StartTimeUnixNano jsonUint64 `json:"startTimeUnixNano"`
	EndTimeUnixNano   jsonUint64 `json:"endTimeUnixNano"`
	Attributes        []keyValue `json:"attributes,omitempty"`
	Events            []event    `json:"events,omitempty"`
	Links             []link     `json:"links,omitempty"`
	Status            status     `json:"status"`
}

type keyValue struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

type anyValue struct {
	StringValue *string    `json:"stringValue,omitempty"`
	BoolValue   *bool      `json:"boolValue,omitempty"`
	IntValue    *jsonInt64 `json:"intValue,omitempty"`
	DoubleValue *float64   `json:"doubleValue,omitempty"`
}

type event struct {
	TimeUnixNano jsonUint64 `json:"timeUnixNano"`
	Name         string     `json:"name"`
	Attributes   []keyValue `json:"attributes,omitempty"`
}

type link struct {
	TraceID string `json:"traceId"`
	SpanID  string `json:"spanId"`
}

type status struct {
	Code    jsonEnum `json:"code,omitempty"`
	Message string   `json:"message,omitempty"`
}

// jsonUint64 encodes as a decimal string and decodes from a string or number.
type jsonUint64 uint64

func (v jsonUint64) MarshalJSON() ([]byte, error) {
	return strconv.AppendQuote(nil, strconv.FormatUint(uint64(v), 10)), nil
}

func (v *jsonUint64) UnmarshalJSON(b []byte) error {
	n, err := strconv.ParseUint(string(unquote(b)), 10, 64)
	*v = jsonUint64(n)
	return err
}

type jsonInt64 int64

func (v jsonInt64) MarshalJSON() ([]byte, error) {
	return strconv.AppendQuote(nil, strconv.FormatInt(int64(v), 10)), nil
}

func (v *jsonInt64) UnmarshalJSON(b []byte) error {
	n, err := strconv.ParseInt(string(unquote(b)), 10, 64)
	*v = jsonInt64(n)
	return err
}

// jsonEnum encodes as an integer and decodes from an integer or a name such
// as SPAN_KIND_CLIENT or STATUS_CODE_ERROR.
type jsonEnum int

func (v jsonEnum) MarshalJSON() ([]byte, error) { return strconv.AppendInt(nil, int64(v), 10), nil }

func (v *jsonEnum) UnmarshalJSON(b []byte) error {
	s := string(unquote(b))
	if n, err := strconv.Atoi(s); err == nil {
		*v = jsonEnum(n)
		return nil
	}
	if n, ok := enumNames[s]; ok {
		*v = jsonEnum(n)
		return nil
	}
	return fmt.Errorf("tracefile: unknown enum value %q", s)
}

var enumNames = map[string]int{
	"SPAN_KIND_UNSPECIFIED": 0, "SPAN_KIND_INTERNAL": 1, "SPAN_KIND_SERVER": 2,
	"SPAN_KIND_CLIENT": 3, "SPAN_KIND_PRODUCER": 4, "SPAN_KIND_CONSUMER": 5,
	"STATUS_CODE_UNSET": 0, "STATUS_CODE_OK": 1, "STATUS_CODE_ERROR": 2,
}

func unquote(b []byte) []byte {
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		return b[1 : len(b)-1]
	}
	return b
}

// encode marshals the span alone; the Writer wraps it in the envelope.
func encode(s *Span) ([]byte, error) {
	if len(s.TraceID) != 32 || len(s.SpanID) != 16 {
		return nil, fmt.Errorf("tracefile: span %q has ids %q/%q; want 32 and 16 hex characters", s.Name, s.TraceID, s.SpanID)
	}
	out := span{
		TraceID: s.TraceID, SpanID: s.SpanID, ParentSpanID: s.ParentSpanID, Name: s.Name, Kind: jsonEnum(s.Kind),
		StartTimeUnixNano: unixNano(s.Start), EndTimeUnixNano: unixNano(s.End),
		Attributes: toKeyValues(s.Attributes),
		Status:     status{Code: jsonEnum(s.Status.Code), Message: s.Status.Message},
	}
	for _, e := range s.Events {
		out.Events = append(out.Events, event{TimeUnixNano: unixNano(e.Time), Name: e.Name, Attributes: toKeyValues(e.Attributes)})
	}
	for _, l := range s.Links {
		out.Links = append(out.Links, link(l))
	}
	return json.Marshal(out)
}

func unixNano(t time.Time) jsonUint64 {
	if t.IsZero() {
		return 0
	}
	return jsonUint64(t.UnixNano())
}

func toKeyValues(attrs []Attribute) []keyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]keyValue, 0, len(attrs))
	for _, a := range attrs {
		var v anyValue
		switch x := a.Value.(type) {
		case string:
			v.StringValue = &x
		case bool:
			v.BoolValue = &x
		case int64:
			iv := jsonInt64(x)
			v.IntValue = &iv
		case int:
			iv := jsonInt64(x)
			v.IntValue = &iv
		case float64:
			v.DoubleValue = &x
		default:
			s := fmt.Sprint(x)
			v.StringValue = &s
		}
		out = append(out, keyValue{Key: a.Key, Value: v})
	}
	return out
}

func fromKeyValues(kvs []keyValue) []Attribute {
	if len(kvs) == 0 {
		return nil
	}
	out := make([]Attribute, 0, len(kvs))
	for _, kv := range kvs {
		a := Attribute{Key: kv.Key}
		switch {
		case kv.Value.StringValue != nil:
			a.Value = *kv.Value.StringValue
		case kv.Value.BoolValue != nil:
			a.Value = *kv.Value.BoolValue
		case kv.Value.IntValue != nil:
			a.Value = int64(*kv.Value.IntValue)
		case kv.Value.DoubleValue != nil:
			a.Value = *kv.Value.DoubleValue
		}
		out = append(out, a)
	}
	return out
}

// Decode parses one line, an OTLP JSON export request, into its spans.
func Decode(line []byte) ([]*Span, error) {
	var req exportRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return nil, fmt.Errorf("tracefile: %w", err)
	}
	var out []*Span
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				s := &Span{
					TraceID: sp.TraceID, SpanID: sp.SpanID, ParentSpanID: sp.ParentSpanID, Name: sp.Name, Kind: Kind(sp.Kind),
					Start: fromUnixNano(sp.StartTimeUnixNano), End: fromUnixNano(sp.EndTimeUnixNano),
					Attributes: fromKeyValues(sp.Attributes),
					Status:     Status{Code: StatusCode(sp.Status.Code), Message: sp.Status.Message},
				}
				for _, e := range sp.Events {
					s.Events = append(s.Events, Event{Name: e.Name, Time: fromUnixNano(e.TimeUnixNano), Attributes: fromKeyValues(e.Attributes)})
				}
				for _, l := range sp.Links {
					s.Links = append(s.Links, Link(l))
				}
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func fromUnixNano(n jsonUint64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(n)).UTC()
}

func attrString(attrs []Attribute, key string) string {
	for _, a := range attrs {
		if a.Key == key {
			if s, ok := a.Value.(string); ok {
				return s
			}
			return fmt.Sprint(a.Value)
		}
	}
	return ""
}

func openFile(path string) (*os.File, error) { return os.Open(path) }
