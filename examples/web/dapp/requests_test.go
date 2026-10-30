package main

import "testing"

// Past the limit, answered requests make room; pending ones are kept, so a
// late answer still finds its entry.
func TestRequestLogKeepsPending(t *testing.T) {
	l := newRequestLog()
	var ids []int64
	for range requestsPerSession + 5 {
		ids = append(ids, l.start("t", "personal_sign", "eip155:1", "0xabc").LocalID)
	}
	if n := len(l.snapshot()["t"]); n != requestsPerSession+5 {
		t.Fatalf("kept %d entries, want every pending one", n)
	}
	for _, id := range ids[:10] {
		l.finish("t", id, nil, "rejected", 5000)
	}
	l.start("t", "personal_sign", "eip155:1", "0xabc")
	list := l.snapshot()["t"]
	if len(list) != requestsPerSession {
		t.Fatalf("kept %d entries, want %d", len(list), requestsPerSession)
	}
	pending := 0
	for _, e := range list {
		if e.Pending {
			pending++
		}
	}
	if pending != 16 {
		t.Fatalf("%d pending entries kept, want 16", pending)
	}
	if e := l.finish("t", ids[len(ids)-1], nil, "rejected", 5000); e.Method != "personal_sign" {
		t.Fatalf("finished entry %+v lost its request", e)
	}
}
