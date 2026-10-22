package walletconnect

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// Several dApp and wallet pairs run at once, each with concurrent requests,
// to shake out races between the API goroutines, the engine and the relay.
func TestE2EConcurrentSessions(t *testing.T) {
	const pairs, requests = 4, 5
	for i := range pairs {
		t.Run(fmt.Sprintf("pair-%d", i), func(t *testing.T) {
			t.Parallel()
			dapp, wallet := newClient(t, "dapp", nil), newClient(t, "wallet", nil)
			topic := connect(t, dapp, wallet)
			ctx := ctxT(t)

			// The wallet echoes each request's params back as its result.
			go func() {
				for ev := range wallet.Events() {
					if r, ok := ev.(EventSessionRequest); ok {
						if err := wallet.Respond(ctx, r.Topic, r.ID, r.Params); err != nil {
							t.Errorf("respond: %v", err)
						}
					}
				}
			}()

			var wg sync.WaitGroup
			for j := range requests {
				wg.Add(1)
				go func() {
					defer wg.Done()
					params := json.RawMessage(fmt.Sprintf(`["pair %d request %d"]`, i, j))
					raw, err := dapp.Request(ctx, RequestParams{Topic: topic, ChainID: chainMainnet, Method: "personal_sign", Params: params})
					if err != nil {
						t.Errorf("request %d: %v", j, err)
						return
					}
					if string(raw) != string(params) {
						t.Errorf("request %d: got %s, want %s", j, raw, params)
					}
				}()
			}
			wg.Wait()
			if err := dapp.Ping(ctx, topic); err != nil {
				t.Errorf("ping: %v", err)
			}
		})
	}
}
