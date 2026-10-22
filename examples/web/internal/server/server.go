// Package server is the HTTP side shared by the demo dApp and wallet: a JSON
// API, a server-sent event stream of SDK and app events, and static files.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"

	wc "github.com/WalletConnect/walletconnect-go"
)

//go:embed assets
var assets embed.FS

type Server struct {
	mux *http.ServeMux
	hub *hub
	log *slog.Logger
}

// New serves page (the app's static directory) at / and shared assets at /assets/.
func New(page fs.FS, log *slog.Logger) *Server {
	s := &Server{mux: http.NewServeMux(), hub: newHub(), log: log}
	s.mux.Handle("GET /", http.FileServerFS(page))
	s.mux.Handle("GET /assets/", http.FileServerFS(assets))
	s.mux.HandleFunc("GET /api/events", s.hub.serve)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// Handle registers a JSON endpoint. fn's result is written as JSON; an error
// becomes {"error": message} with a status derived from the SDK error.
func (s *Server) Handle(pattern string, fn func(r *http.Request) (any, error)) {
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		out, err := fn(r)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			s.log.Warn("api error", "path", r.URL.Path, "err", err)
			w.WriteHeader(statusOf(err))
			out = map[string]string{"error": err.Error()}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
}

// Decode reads a JSON request body into v.
func Decode[T any](r *http.Request) (T, error) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		return v, &badRequest{err}
	}
	return v, nil
}

type badRequest struct{ err error }

func (b *badRequest) Error() string { return "bad request: " + b.err.Error() }

func statusOf(err error) int {
	var bad *badRequest
	var rpc *wc.RPCError
	switch {
	case errors.As(err, &bad), errors.Is(err, wc.ErrInvalidParams), errors.Is(err, wc.ErrPairingExists), errors.Is(err, wc.ErrExpired):
		return http.StatusBadRequest
	case errors.Is(err, wc.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, wc.ErrUnauthorized):
		return http.StatusForbidden
	case errors.As(err, &rpc):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// Publish sends an app event to every open page.
func (s *Server) Publish(kind string, data any) { s.hub.publish(kind, data) }

// PumpEvents forwards SDK events to the pages until the client closes.
func (s *Server) PumpEvents(ctx context.Context, events <-chan wc.Event) {
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			kind, data := describe(ev)
			s.Publish(kind, data)
		case <-ctx.Done():
			return
		}
	}
}

// Async runs fn in the background and publishes its outcome as kind, merged
// with base. Long protocol waits (wallet answers, acks) use it so HTTP calls
// return at once.
func (s *Server) Async(kind string, base map[string]any, fn func() (any, error)) {
	go func() {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		result, err := fn()
		if err != nil {
			out["error"], out["code"] = ErrorDetails(err)
		} else {
			out["result"] = result
		}
		s.Publish(kind, out)
	}()
}

// ErrorDetails splits an error into a display message and, for a peer's
// JSON-RPC error, its code.
func ErrorDetails(err error) (string, int) {
	var rpc *wc.RPCError
	if errors.As(err, &rpc) {
		return rpc.Message, rpc.Code
	}
	return err.Error(), 0
}
