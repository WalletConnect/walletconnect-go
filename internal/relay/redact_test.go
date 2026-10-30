package relay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testJWT = "eyJhbGciOiJFZERTQSIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJkaWQ6a2V5OnoifQ.c2lnbmF0dXJlLXNpZ25hdHVyZS1zaWduYXR1cmU"

func TestRedactDial(t *testing.T) {
	dialURL := "wss://relay.walletconnect.org/?auth=" + testJWT + "&projectId=0123456789abcdef0123456789abcdef&ua=wc-2%2Fgo-1.0.0%2Flinux%2Fserver"
	// coder/websocket dials over https and wraps the http client's error,
	// which quotes the request URL.
	inner := &url.Error{Op: "Get", URL: strings.Replace(dialURL, "wss://", "https://", 1), Err: errors.New("dial tcp 127.0.0.1:443: connection refused")}
	err := redactDial(fmt.Errorf("failed to WebSocket dial: %w", inner), dialURL)

	msg := err.Error()
	for _, secret := range []string{testJWT, "0123456789abcdef0123456789abcdef", "auth="} {
		if strings.Contains(msg, secret) {
			t.Fatalf("message still holds %q:\n%s", secret, msg)
		}
	}
	if !strings.Contains(msg, "https://relay.walletconnect.org/?<redacted>") || !strings.Contains(msg, "connection refused") {
		t.Fatalf("message lost its shape:\n%s", msg)
	}
	var ue *url.Error
	if !errors.As(err, &ue) || ue != inner {
		t.Fatal("the original error is no longer reachable with errors.As")
	}

	plain := errors.New("no url here")
	if redactDial(plain, dialURL) != plain {
		t.Fatal("an error without the query should be returned as is")
	}
	if redactDial(nil, dialURL) != nil {
		t.Fatal("nil in, nil out")
	}
	if redactDial(inner, "wss://relay.walletconnect.org/") != inner {
		t.Fatal("a URL without a query redacts nothing")
	}
}

// A dial failure reaches the application's state event and the log
// without the relay URL's query, which carries the auth token. Offline: the
// dial goes to a closed local port.
func TestDialErrorHidesAuth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	var logBuf bytes.Buffer
	errs := make(chan error, 10)
	c := New(Config{
		URL: "wss://127.0.0.1:1", ProjectID: "0123456789abcdef0123456789abcdef", UserAgent: "wc-2/go-test/test/test", Key: priv,
		Topics:  func(context.Context) ([]string, error) { return nil, nil },
		Deliver: func(Incoming) {},
		OnState: func(connected bool, err error) {
			if !connected && err != nil {
				errs <- err
			}
		},
		Logger:  slog.New(slog.NewTextHandler(&logBuf, nil)),
		Backoff: []time.Duration{50 * time.Millisecond},
	})
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	c.Connect()
	var err error
	select {
	case err = <-errs:
	case <-time.After(5 * time.Second):
		t.Fatal("no dial failure reported")
	}
	cancel()
	<-done
	for _, secret := range []string{"auth=", "eyJ", "projectId=0123456789abcdef0123456789abcdef"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("state event error holds %q: %s", secret, err)
		}
		if strings.Contains(logBuf.String(), secret) {
			t.Errorf("log holds %q:\n%s", secret, logBuf.String())
		}
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Errorf("error should show where the query was: %s", err)
	}
}
