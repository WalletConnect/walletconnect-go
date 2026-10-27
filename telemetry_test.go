package walletconnect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/pulse"
)

func pulseServer(t *testing.T) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

func offlineClient(t *testing.T, o Options) *Client {
	t.Helper()
	o.ProjectID = "project"
	o.Logger = quietLogger()
	o.Metadata = Metadata{Name: "go-telemetry", URL: "https://wallet.example"}
	c, err := New(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A failed API call reaches Pulse as an ERROR event with its error type,
// and the pairing URI it was given, key included, never does.
func TestTelemetryReportsFailureWithoutTheURI(t *testing.T) {
	srv, bodies := pulseServer(t)
	c := offlineClient(t, Options{pulseURL: srv.URL})
	k, err := crypto.RandomKey()
	if err != nil {
		t.Fatal(err)
	}
	symKey := crypto.EncodeKey(k)
	expired := "wc:" + crypto.Topic(k) + "@2?relay-protocol=irn&symKey=" + symKey + "&expiryTimestamp=1"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Pair(ctx, expired); err == nil {
		t.Fatal("expired uri paired")
	}
	c.Close()

	got := bodies()
	if len(got) != 1 {
		t.Fatalf("%d batches sent, want 1", len(got))
	}
	if strings.Contains(got[0], symKey) {
		t.Fatalf("the pairing key reached Pulse: %s", got[0])
	}
	var evs []pulse.Event
	if err := json.Unmarshal([]byte(got[0]), &evs); err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Props.Event != "ERROR" || evs[0].Props.Type != "expired" ||
		evs[0].Props.Properties.Action != "pair" || evs[0].Domain != "https://wallet.example" {
		t.Fatalf("events %+v", evs)
	}
}

func TestTelemetryIsOffWithoutAnAppURL(t *testing.T) {
	srv, _ := pulseServer(t)
	c, err := New(context.Background(), Options{ProjectID: "project", Logger: quietLogger(), pulseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.pulse != nil {
		t.Fatal("reporting without an origin Pulse would accept")
	}
}

func TestTelemetryIsOffInTestsAndWithNoTelemetry(t *testing.T) {
	if c := offlineClient(t, Options{}); c.pulse != nil {
		t.Error("test binaries must not report by default")
	} else {
		c.Close()
	}
	srv, bodies := pulseServer(t)
	c := offlineClient(t, Options{pulseURL: srv.URL, NoTelemetry: true})
	if c.pulse != nil {
		t.Error("NoTelemetry did not turn reporting off")
	}
	_ = c.Pair(context.Background(), "nope")
	c.Close()
	if n := len(bodies()); n != 0 {
		t.Fatalf("%d batches sent with NoTelemetry", n)
	}
}
