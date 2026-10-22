package walletconnect

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/WalletConnect/walletconnect-go/storage"
)

// TestE2ESoakNoLeaks runs many full session lifecycles in one process and
// checks that goroutines, heap, engine state and storage return to their
// baseline. Opt in with TEST_SOAK=<cycles>.
func TestE2ESoakNoLeaks(t *testing.T) {
	cycles, _ := strconv.Atoi(os.Getenv("TEST_SOAK"))
	if cycles <= 0 {
		t.Skip("set TEST_SOAK=<cycles> to run the soak test")
	}
	dappKV, walletKV := storage.NewMemory(), storage.NewMemory()
	dapp, wallet := newClient(t, "dapp", dappKV), newClient(t, "wallet", walletKV)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cycles)*30*time.Second)
	defer cancel()

	cycle := func(i int) {
		topic := connect(t, dapp, wallet)
		result := requestAsync(ctx, dapp, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "personal_sign"})
		req := next[EventSessionRequest](t, wallet)
		if err := wallet.Respond(ctx, topic, req.ID, "0xsig"); err != nil {
			t.Fatalf("cycle %d respond: %v", i, err)
		}
		if r := <-result; r.err != nil {
			t.Fatalf("cycle %d request: %v", i, r.err)
		}
		ack, err := wallet.Update(ctx, topic, evmSession(chainMainnet))
		if err != nil {
			t.Fatalf("cycle %d update: %v", i, err)
		}
		if err := ack.Wait(ctx); err != nil {
			t.Fatalf("cycle %d update ack: %v", i, err)
		}
		if err := wallet.Emit(ctx, topic, chainMainnet, "accountsChanged", []string{address}); err != nil {
			t.Fatalf("cycle %d emit: %v", i, err)
		}
		next[EventSessionEvent](t, dapp)
		if err := dapp.Ping(ctx, topic); err != nil {
			t.Fatalf("cycle %d ping: %v", i, err)
		}
		closer, peer := dapp, wallet
		if i%2 == 1 {
			closer, peer = wallet, dapp
		}
		if err := closer.Disconnect(ctx, topic); err != nil {
			t.Fatalf("cycle %d disconnect: %v", i, err)
		}
		next[EventSessionDelete](t, peer)
	}
	drainEvents := func() {
		for _, c := range []*tclient{dapp, wallet} {
			for len(c.Events()) > 0 || c.queuedEvents() > 0 {
				<-c.Events()
			}
		}
	}
	snapshot := func() (int, uint64) {
		drainEvents()
		time.Sleep(time.Second) // let fire-and-forget sends and handlers settle
		drainEvents()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return runtime.NumGoroutine(), m.HeapAlloc
	}

	cycle(0) // warm up lazily started goroutines, pools and the relay socket
	baseGoroutines, baseHeap := snapshot()
	start := time.Now()
	for i := 1; i <= cycles; i++ {
		cycle(i)
	}
	goroutines, heap := snapshot()
	t.Logf("%d cycles in %v: goroutines %d -> %d, heap %.1f MB -> %.1f MB",
		cycles, time.Since(start).Round(time.Second), baseGoroutines, goroutines, float64(baseHeap)/1e6, float64(heap)/1e6)

	if goroutines > baseGoroutines+4 {
		t.Errorf("goroutines grew from %d to %d", baseGoroutines, goroutines)
	}
	if heap > baseHeap+8<<20 {
		t.Errorf("heap grew from %d to %d bytes", baseHeap, heap)
	}
	// Answered wallet pairings are retired, not deleted, until their URI
	// expires (5 minutes), so the URI cannot be paired again. Expire them.
	for _, c := range []*tclient{dapp, wallet} {
		c.clock.advance(6 * time.Minute)
		if err := c.sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []*tclient{dapp, wallet} {
		if s := c.engineStats(); s != (engineStatsZero) {
			t.Errorf("%s engine state not cleared: %+v", c.name, s)
		}
		keys := map[string]int{}
		_ = c.kv.View(ctx, func(tx storage.Tx) error {
			return tx.Scan("", func(k string, _ []byte) error {
				prefix := k
				for j, r := range k {
					if r == '/' {
						prefix = k[:j]
						break
					}
				}
				keys[prefix]++
				return nil
			})
		})
		delete(keys, "identity")
		if len(keys) > 0 {
			t.Errorf("%s storage not cleared after disconnecting every session and expiring pairings: %v", c.name, keys)
		}
	}
}
