package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// hub fans events out to server-sent event streams. It replays the latest
// relay_state to each new page, which would otherwise miss a connect that
// happened before it loaded.
type hub struct {
	mu        sync.Mutex
	subs      map[chan []byte]struct{}
	lastRelay []byte
}

func newHub() *hub { return &hub{subs: map[chan []byte]struct{}{}} }

func (h *hub) publish(kind string, data any) {
	b, err := json.Marshal(map[string]any{"type": kind, "data": data, "at": time.Now().UnixMilli()})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if kind == "relay_state" {
		h.lastRelay = b
	}
	for ch := range h.subs {
		select {
		case ch <- b:
		default: // a stalled page misses events; it reloads state on the next one
		}
	}
}

func (h *hub) serve(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	if h.lastRelay != nil {
		ch <- h.lastRelay
	}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	for {
		select {
		case b := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
