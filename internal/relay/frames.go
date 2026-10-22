package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/trace"
)

// relayFrame is any JSON-RPC frame from the relay: a delivery request or a
// response to one of our calls.
type relayFrame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *jsonrpc.Error  `json:"error"`
}

func parseFrame(data []byte) (relayFrame, bool) {
	var f relayFrame
	return f, json.Unmarshal(data, &f) == nil
}

func (r *runner) handleFrame(ctx context.Context, data []byte) {
	f, ok := parseFrame(data)
	if !ok {
		r.c.log.Warn("malformed relay frame")
		return
	}
	if f.Method != "" {
		r.handleDelivery(ctx, f)
		return
	}
	r.handleResponse(f)
}

// handleResponseFrame handles only responses; drain uses it, since a
// delivery arriving during Close is left unacked for the relay to redeliver.
func (r *runner) handleResponseFrame(data []byte) {
	if f, ok := parseFrame(data); ok && f.Method == "" {
		r.handleResponse(f)
	}
}

func (r *runner) handleDelivery(ctx context.Context, f relayFrame) {
	if !strings.HasSuffix(f.Method, "_subscription") {
		_ = r.write(ctx, mustJSON(map[string]any{"id": f.ID, "jsonrpc": jsonrpc.Version,
			"error": jsonrpc.Error{Code: -32601, Message: "Method not found"}}))
		return
	}
	var p struct {
		Data struct {
			Topic       string `json:"topic"`
			Message     string `json:"message"`
			PublishedAt int64  `json:"publishedAt"`
			Tag         int    `json:"tag"`
		} `json:"data"`
	}
	if err := json.Unmarshal(f.Params, &p); err != nil {
		r.c.log.Warn("malformed subscription", "err", err)
		return
	}
	span := r.c.cfg.Tracer.Start("incoming", "relayId", string(f.ID), "socket", r.gen, "topic", p.Data.Topic,
		"tag", p.Data.Tag, "publishedAt", publishedAt(p.Data.PublishedAt))
	r.c.cfg.Deliver(Incoming{ID: f.ID, Gen: r.gen, Span: span, Topic: p.Data.Topic, Message: p.Data.Message,
		Tag: p.Data.Tag, PublishedAt: p.Data.PublishedAt})
}

func (r *runner) handleResponse(f relayFrame) {
	var id int64
	if err := json.Unmarshal(f.ID, &id); err != nil {
		return
	}
	cl, ok := r.pending[id]
	if !ok {
		return
	}
	delete(r.pending, id)
	reply := []any{"method", cl.method, "relayId", id, "took", time.Since(cl.sentAt).Round(time.Millisecond)}
	if f.Error != nil {
		reply = append(reply, "relayError", f.Error.Message)
	}
	trace.From(cl.ctx).Step("relay reply", reply...)
	if f.Error != nil {
		r.subscribeFailed(cl)
		r.finish(cl, nil, f.Error)
		return
	}
	r.finish(cl, f.Result, nil)
}

// publishedAt formats the relay's publish time, which it sends in unix
// seconds (milliseconds are accepted too, for safety).
func publishedAt(v int64) string {
	t := time.Unix(v, 0)
	if v > 1e12 {
		t = time.UnixMilli(v)
	}
	return t.Format(time.RFC3339)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("relay: marshal: %v", err))
	}
	return b
}
