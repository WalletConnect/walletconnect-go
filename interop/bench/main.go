// Command bench measures connect / sign / disconnect between two Go clients
// on the live relay, matching interop/js/bench.mjs, and prints one JSON
// object with per-phase timings in ms.
//
//	TEST_PROJECT_ID=... go run ./interop/bench [-n iterations]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"log/slog"
	"os"
	"runtime"
	"syscall"
	"time"

	wc "github.com/WalletConnect/walletconnect-go"
)

func main() {
	n := flag.Int("n", 30, "measured iterations, after one warm-up")
	flag.Parse()
	relayURL := os.Getenv("TEST_RELAY_URL")
	if relayURL == "" {
		relayURL = "wss://relay.walletconnect.com"
	}
	ctx := context.Background()
	newClient := func(name string) *wc.Client {
		c, err := wc.New(ctx, wc.Options{
			ProjectID: os.Getenv("TEST_PROJECT_ID"), RelayURL: relayURL,
			Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
			Metadata: wc.Metadata{Name: "go-bench-" + name, Description: "bench", URL: "https://example.com"},
		})
		if err != nil {
			log.Fatal(err)
		}
		return c
	}

	start := time.Now()
	dapp, wallet := newClient("dapp"), newClient("wallet")
	initMs := ms(time.Since(start))
	defer dapp.Close()
	defer wallet.Close()

	deleted := make(chan struct{}, 1)
	go walletLoop(ctx, wallet, deleted)
	go func() {
		for range dapp.Events() { // keep the dApp's event queue drained
		}
	}()

	cycle := func() map[string]float64 {
		t := map[string]float64{}
		start := time.Now()
		conn, err := dapp.Connect(ctx, wc.ConnectParams{OptionalNamespaces: map[string]wc.ProposalNamespace{
			"eip155": {Chains: []string{"eip155:1"}, Methods: []string{"personal_sign"}, Events: []string{"chainChanged"}},
		}})
		check(err)
		check(wallet.Pair(ctx, conn.URI))
		session, err := conn.Wait(ctx)
		check(err)
		t["connect"] = ms(time.Since(start))

		start = time.Now()
		_, err = dapp.Request(ctx, wc.RequestParams{Topic: session.Topic, ChainID: "eip155:1", Method: "personal_sign",
			Params: json.RawMessage(`["0x68656c6c6f","0xab16a96D359eC26a11e2C2b3d8f8B8942d5Bfcdb"]`)})
		check(err)
		t["sign"] = ms(time.Since(start))

		start = time.Now()
		check(dapp.Disconnect(ctx, session.Topic))
		<-deleted
		t["disconnect"] = ms(time.Since(start))
		return t
	}

	cycle() // warm-up
	phases := map[string][]float64{}
	for range *n {
		for k, v := range cycle() {
			phases[k] = append(phases[k], v)
		}
	}

	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	maxRSS := float64(ru.Maxrss) / 1024 // KB on Linux
	if runtime.GOOS == "darwin" {
		maxRSS /= 1024 // bytes on macOS
	}
	cpu := time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"sdk": "go " + wc.Version, "iterations": *n, "initMs": initMs, "phases": phases,
		"maxRssMB": maxRSS, "cpuMs": ms(cpu),
	})
}

// walletLoop approves every proposal and answers every request.
func walletLoop(ctx context.Context, w *wc.Client, deleted chan<- struct{}) {
	ns := map[string]wc.SessionNamespace{"eip155": {
		Chains: []string{"eip155:1"}, Methods: []string{"personal_sign"}, Events: []string{"chainChanged", "accountsChanged"},
		Accounts: []string{"eip155:1:0xab16a96D359eC26a11e2C2b3d8f8B8942d5Bfcdb"},
	}}
	for ev := range w.Events() {
		switch e := ev.(type) {
		case wc.EventSessionProposal:
			go func() {
				if _, err := w.Approve(ctx, wc.ApproveParams{ProposalID: e.Proposal.ID, Namespaces: ns}); err != nil {
					log.Print(err)
				}
			}()
		case wc.EventSessionRequest:
			go func() {
				if err := w.Respond(ctx, e.Topic, e.ID, "0xsig"); err != nil {
					log.Print(err)
				}
			}()
		case wc.EventSessionDelete:
			deleted <- struct{}{}
		}
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
