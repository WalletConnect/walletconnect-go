package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	wc "github.com/WalletConnect/walletconnect-go"
	"github.com/WalletConnect/walletconnect-go/examples/web/internal/server"
)

// Malformed calls are refused up front, not accepted and failed later.
func TestAPIRefusesMalformedCalls(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := wc.New(context.Background(), wc.Options{ProjectID: "offline", RelayURL: "wss://127.0.0.1:1",
		Logger: log, NoTelemetry: true, Metadata: wc.Metadata{Name: "test-dapp"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	srv := server.New(fstest.MapFS{}, log)
	registerAPI(srv, c)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/api/ping", `{"topic":""}`, http.StatusBadRequest},
		{"/api/ping", `{"topic":"nope"}`, http.StatusNotFound},
		{"/api/connect", ``, http.StatusBadRequest},
		{"/api/connect", `{}`, http.StatusBadRequest},
	} {
		res, err := client.Post(ts.URL+tc.path, "application/json", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("POST %s %s: %d, want %d", tc.path, tc.body, res.StatusCode, tc.want)
		}
	}
}
