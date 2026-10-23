package tracefile

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// Summary is what a trace file adds up to: how many spans of each name,
// how they ended and how long they took, with requests and answers broken
// down by method.
type Summary struct {
	Spans    int
	From, To time.Time
	// Stats has one entry per span name, sorted by name, each with its
	// method breakdown in Methods.
	Stats []*Stat
	// Reconnects counts relay.reconnect spans by reason.
	Reconnects map[string]int
	// Dropped counts relay.incoming spans dropped without handling, by reason.
	Dropped map[string]int
	// Errors counts lines that did not parse.
	Errors int
}

// Stat is the count and latency of spans with one name, or of one method
// under a name.
type Stat struct {
	Name   string
	Method string // "" for the name total
	Count  int
	OK     int // status ok or unset
	Failed int // status error
	// Outcomes counts the wc.outcome attribute, such as ok, timeout or
	// peer_error, when spans carry one.
	Outcomes      map[string]int
	P50, P95, Max time.Duration
	Methods       []*Stat // sorted by method

	durations []time.Duration
}

// Summarize reads spans from r and adds them up.
func Summarize(r io.Reader) (*Summary, error) {
	sum := &Summary{Reconnects: map[string]int{}, Dropped: map[string]int{}}
	byName := map[string]*Stat{}
	for s, err := range Read(r) {
		if err != nil {
			sum.Errors++
			continue
		}
		sum.add(byName, s)
	}
	for _, st := range byName {
		st.finish()
		sum.Stats = append(sum.Stats, st)
	}
	sort.Slice(sum.Stats, func(i, j int) bool { return sum.Stats[i].Name < sum.Stats[j].Name })
	return sum, nil
}

// SummarizeFiles summarizes the files in order, as returned by Files.
func SummarizeFiles(paths ...string) (*Summary, error) {
	readers := make([]io.Reader, 0, len(paths))
	for _, p := range paths {
		f, err := openFile(p)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		readers = append(readers, f)
	}
	return Summarize(io.MultiReader(readers...))
}

func (sum *Summary) add(byName map[string]*Stat, s *Span) {
	sum.Spans++
	if sum.From.IsZero() || s.Start.Before(sum.From) {
		sum.From = s.Start
	}
	if s.End.After(sum.To) {
		sum.To = s.End
	}
	st := byName[s.Name]
	if st == nil {
		st = &Stat{Name: s.Name}
		byName[s.Name] = st
	}
	st.add(s)
	if m := methodKey(s); m != "" {
		i := slices.IndexFunc(st.Methods, func(x *Stat) bool { return x.Method == m })
		if i < 0 {
			st.Methods = append(st.Methods, &Stat{Name: s.Name, Method: m})
			i = len(st.Methods) - 1
		}
		st.Methods[i].add(s)
	}
	switch s.Name {
	case "relay.reconnect":
		sum.Reconnects[orUnknown(s.Attr("wc.reason"))]++
	case "relay.incoming":
		for _, e := range s.Events {
			if e.Name == "dropped" {
				sum.Dropped[orUnknown(attrString(e.Attributes, "wc.reason"))]++
			}
		}
	}
}

// methodKey is the span's method breakdown: the session request method
// under the protocol method for peer answers, the method alone for API
// calls. Relay calls have one method each, so none.
func methodKey(s *Span) string {
	if strings.HasPrefix(s.Name, "relay.") {
		return ""
	}
	rpc, inner := s.Attr("rpc.method"), s.Attr("wc.method")
	switch {
	case rpc != "" && inner != "":
		return rpc + "/" + inner
	case rpc != "":
		return rpc
	default:
		return inner
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func (st *Stat) add(s *Span) {
	st.Count++
	if s.Status.Code == StatusError {
		st.Failed++
	} else {
		st.OK++
	}
	if o := s.Attr("wc.outcome"); o != "" {
		if st.Outcomes == nil {
			st.Outcomes = map[string]int{}
		}
		st.Outcomes[o]++
	}
	if d := s.Duration(); d > 0 {
		st.durations = append(st.durations, d)
	}
}

func (st *Stat) finish() {
	slices.Sort(st.durations)
	if n := len(st.durations); n > 0 {
		st.P50 = st.durations[(n-1)/2]
		st.P95 = st.durations[(n*95+99)/100-1]
		st.Max = st.durations[n-1]
	}
	st.durations = nil
	sort.Slice(st.Methods, func(i, j int) bool { return st.Methods[i].Method < st.Methods[j].Method })
	for _, m := range st.Methods {
		m.finish()
	}
}

// Format renders the summary as a text table.
func (sum *Summary) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d spans", sum.Spans)
	if !sum.From.IsZero() {
		fmt.Fprintf(&b, " from %s to %s", sum.From.UTC().Format(time.RFC3339), sum.To.UTC().Format(time.RFC3339))
	}
	if sum.Errors > 0 {
		fmt.Fprintf(&b, ", %d unreadable lines", sum.Errors)
	}
	b.WriteString("\n\n")
	width := len("span")
	for _, st := range sum.Stats {
		width = max(width, len(st.Name))
		for _, m := range st.Methods {
			width = max(width, len(m.Method)+2)
		}
	}
	// Names are padded by hand so that right alignment leaves them flush left;
	// the outcomes follow the last tab, outside the aligned columns.
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintf(tw, "%-*s\tcount\tok\tfailed\tp50\tp95\tmax\t  outcomes\n", width, "span")
	for _, st := range sum.Stats {
		st.row(tw, width, st.Name)
		for _, m := range st.Methods {
			m.row(tw, width, "  "+m.Method)
		}
	}
	tw.Flush()
	if len(sum.Reconnects) > 0 {
		fmt.Fprintf(&b, "\nreconnects: %s\n", counts(sum.Reconnects))
	}
	if len(sum.Dropped) > 0 {
		fmt.Fprintf(&b, "dropped: %s\n", counts(sum.Dropped))
	}
	return b.String()
}

func (st *Stat) row(w io.Writer, width int, label string) {
	fmt.Fprintf(w, "%-*s\t%d\t%d\t%d\t%s\t%s\t%s\t", width, label, st.Count, st.OK, st.Failed, short(st.P50), short(st.P95), short(st.Max))
	if len(st.Outcomes) > 0 {
		fmt.Fprintf(w, "  %s", counts(st.Outcomes))
	}
	fmt.Fprintln(w)
}

func short(d time.Duration) string {
	if d == 0 {
		return ""
	}
	switch {
	case d < time.Millisecond:
		return d.Round(time.Microsecond).String()
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	default:
		return d.Round(10 * time.Millisecond).String()
	}
}

func counts(m map[string]int) string {
	keys := slices.Sorted(maps.Keys(m))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}
