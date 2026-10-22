package walletconnect

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/WalletConnect/walletconnect-go/internal/testenv"
	"github.com/WalletConnect/walletconnect-go/storage"
)

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) lines(traceID string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, l := range strings.Split(b.buf.String(), "\n") {
		if strings.Contains(l, " trace="+traceID+" ") {
			out = append(out, l)
		}
	}
	return out
}

func newTracedClient(t *testing.T, name string, sink *syncBuffer) *tclient {
	t.Helper()
	url, project := testenv.Relay(t)
	kv := storage.NewMemory()
	c, err := New(context.Background(), Options{
		ProjectID: project, RelayURL: url, Storage: kv, Trace: true,
		Logger:   slog.New(slog.NewTextHandler(sink, nil)),
		Metadata: Metadata{Name: "go-" + name, URL: "https://example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &tclient{Client: c, kv: kv, clock: &clock{}, name: name}
}

var stepRe = regexp.MustCompile(`step=("[^"]*"|\S+)`)

func steps(lines []string) []string {
	var out []string
	for _, l := range lines {
		if m := stepRe.FindStringSubmatch(l); m != nil {
			out = append(out, strings.Trim(m[1], `"`))
		}
	}
	return out
}

// A traced request is logged from the API call through the relay publish and
// the wallet's answer, which links to the incoming message that carried it.
func TestE2ETraceRequest(t *testing.T) {
	var dappLog syncBuffer
	dapp := newTracedClient(t, "dapp", &dappLog)
	wallet := newClient(t, "wallet", nil)
	topic := connect(t, dapp, wallet)
	ctx := ctxT(t)

	result := requestAsync(ctx, dapp, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "personal_sign"})
	req := next[EventSessionRequest](t, wallet)
	if err := wallet.Respond(ctx, topic, req.ID, "0xsig"); err != nil {
		t.Fatal(err)
	}
	if r := <-result; r.err != nil {
		t.Fatal(r.err)
	}

	// Find the request's trace by its start line.
	var reqTrace string
	for _, l := range strings.Split(func() string { dappLog.mu.Lock(); defer dappLog.mu.Unlock(); return dappLog.buf.String() }(), "\n") {
		if strings.Contains(l, "action=request") && strings.Contains(l, "step=start") {
			reqTrace = regexp.MustCompile(`trace=(\w+)`).FindStringSubmatch(l)[1]
		}
	}
	if reqTrace == "" {
		t.Fatal("no request trace logged")
	}
	// The relay confirms a publish only after the receiver acks it, so the
	// wallet's answer can arrive before the confirmation: their order varies.
	got := steps(dappLog.lines(reqTrace))
	for _, want := range [][]string{
		{"start", "relay send", "relay reply", "request published", "done"},
		{"start", "relay send", "peer answered", "done"},
	} {
		if !containsInOrder(got, want) {
			t.Fatalf("request trace steps = %v, want in order %v", got, want)
		}
	}

	// The answer links to the incoming trace that delivered it.
	answered := dappLog.lines(reqTrace)
	var via string
	for _, l := range answered {
		if m := regexp.MustCompile(`step="peer answered".* via=(\w+)`).FindStringSubmatch(l); m != nil {
			via = m[1]
		}
	}
	if via == "" {
		t.Fatalf("peer answer not linked to an incoming trace:\n%s", strings.Join(answered, "\n"))
	}
	incoming := steps(dappLog.lines(via))
	if !containsInOrder(incoming, []string{"start", "decrypted", "handled", "ack queued", "done"}) {
		t.Fatalf("incoming trace steps = %v", incoming)
	}
	if !strings.Contains(strings.Join(dappLog.lines(via), "\n"), "acked=true") {
		t.Fatalf("incoming trace does not record the ack:\n%s", strings.Join(dappLog.lines(via), "\n"))
	}
}

func containsInOrder(got, want []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}
