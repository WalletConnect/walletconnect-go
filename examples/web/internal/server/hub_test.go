package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A stream ends after maxAge even though the client keeps it open, and asks
// the page to reopen it within a second.
func TestStreamEndsAfterMaxAge(t *testing.T) {
	h := newHub()
	h.maxAge = 50 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(h.serve))
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("stream did not end: %v", err)
	}
	if !strings.HasPrefix(string(body), "retry: 1000\n") {
		t.Fatalf("stream %q, want a retry hint first", body)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) != 0 {
		t.Fatalf("%d subscribers left after the stream ended", len(h.subs))
	}
}
