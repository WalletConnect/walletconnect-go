# walletconnect-go

A Go SDK for the [WalletConnect Sign v2](https://specs.walletconnect.com/2.0/specs/clients/sign/) protocol. One client can act as a **dApp**, proposing sessions and sending requests, and as a **wallet**, approving sessions and answering them. It interoperates with the JS SDK (`@walletconnect/sign-client`) over the production relay.

Status: `v0.1.0`, experimental. The API may change before `v1`.

```sh
go get github.com/WalletConnect/walletconnect-go
```

Requires Go 1.26 or later and a project ID from the WalletConnect dashboard.

## dApp

```go
client, err := walletconnect.New(ctx, walletconnect.Options{
	ProjectID: projectID,
	Metadata:  walletconnect.Metadata{Name: "My dApp", URL: "https://example.com"},
})
defer client.Close()

conn, err := client.Connect(ctx, walletconnect.ConnectParams{
	OptionalNamespaces: map[string]walletconnect.ProposalNamespace{
		"eip155": {Chains: []string{"eip155:1"}, Methods: []string{"personal_sign"}, Events: []string{"accountsChanged"}},
	},
})
fmt.Println(conn.URI) // show as a QR code or deep link

session, err := conn.Wait(ctx)

sig, err := client.Request(ctx, walletconnect.RequestParams{
	Topic: session.Topic, ChainID: "eip155:1", Method: "personal_sign",
	Params: json.RawMessage(`["0x68656c6c6f", "0xYourAddress"]`),
})
```

A wallet's error answer comes back as `*walletconnect.RPCError`.

## Wallet

```go
err := client.Pair(ctx, uri)

for ev := range client.Events() {
	switch e := ev.(type) {
	case walletconnect.EventSessionProposal:
		approval, err := client.Approve(ctx, walletconnect.ApproveParams{
			ProposalID: e.Proposal.ID,
			Namespaces: map[string]walletconnect.SessionNamespace{
				"eip155": {Accounts: []string{"eip155:1:0xYourAddress"}, Methods: []string{"personal_sign"}, Events: []string{"accountsChanged"}},
			},
		})
		_ = approval.Acknowledged(ctx)
	case walletconnect.EventSessionRequest:
		_ = client.Respond(ctx, e.Topic, e.ID, "0xsignature")
		// or client.RespondError(ctx, e.Topic, e.ID, walletconnect.SDKError(walletconnect.ErrKeyUserRejected, ""))
	}
}
```

Pairing the same URI again before answering re-emits its proposal, for example after the user closes the approval screen. Once the proposal is answered, the URI returns `ErrPairingExists`.

Wallets can also `Reject` a proposal, `Update` the session namespaces and `Emit` events such as `accountsChanged`. Either side can `Extend`, `Ping` and `Disconnect`.

`Update` and `Extend` return as soon as the relay accepts the message, like the JS SDK. Call `Wait` on the returned `Acknowledgement` if you need the peer's confirmation. The change applies locally at once, so a peer that is offline receives it from the relay mailbox when it reconnects.

For a browser UI, [`examples/web`](examples/web) has a demo dApp and wallet that run the SDK server-side. The wallet signs with a test key and the dApp verifies signatures, so they work against real wallets and dApps too.

Command-line versions live in [`examples/dapp`](examples/dapp) and [`examples/wallet`](examples/wallet):

```sh
WC_PROJECT_ID=... go run ./examples/dapp
WC_PROJECT_ID=... go run ./examples/wallet -uri 'wc:...'
```

## Events

`Client.Events()` delivers every event in order and never drops one while the client runs. Events are not persisted. If the process exits before reading them, the state behind them is still available from `Sessions`, `Proposals` and `PendingRequests`.

| Event | Who receives it |
|---|---|
| `EventSessionProposal` | wallet, after `Pair` |
| `EventSessionConnect` | dApp, when the session settles (also returned by `Connection.Wait`) |
| `EventSessionRequest` | wallet |
| `EventSessionEvent`, `EventSessionUpdate` | dApp |
| `EventSessionExtend`, `EventSessionPing`, `EventSessionDelete` | the peer of whoever sent them |
| `EventSessionExpire`, `EventProposalExpire`, `EventRequestExpire` | the side holding the expired record |
| `EventRelayState` | both, on relay connect and disconnect |

## Tracing

Set `Options.Trace` to follow every action from start to finish. Each API call and each incoming relay message gets a trace ID, and every relay send, reply, delivery and ack is logged under it with the time since the action started. An answer from the peer is logged under the request's trace, with `via` naming the incoming trace that carried it. Trace lines go to `Options.Logger` at info level.

A `personal_sign` request answered by a wallet, from the dApp's log:

```text
trace=a0d8eccb action=request step=start              elapsed=1µs      method=personal_sign chainId=eip155:1
trace=a0d8eccb action=request step="relay send"       elapsed=4.5ms    method=irn_publish relayId=1792574820232047044 tag=1108
trace=a0d8eccb action=request step="relay reply"      elapsed=80.6ms   method=irn_publish took=76ms
trace=a0d8eccb action=request step="request published" elapsed=80.6ms  id=1792574820228847
trace=8bf5ecff action=incoming step=start             elapsed=0s       relayId=458899154384782 tag=1109
trace=8bf5ecff action=incoming step=decrypted         elapsed=83µs     id=1792574820228847 kind=response
trace=8bf5ecff action=incoming step=handled           elapsed=4.8ms
trace=a0d8eccb action=request step="peer answered"    elapsed=1.602s   via=8bf5ecff outcome=ok
trace=a0d8eccb action=request step=done              elapsed=1.602s
trace=8bf5ecff action=incoming step=done              elapsed=4.9ms    acked=true
```

The relay confirms a publish only after the receiver acks it, so the `took` of an `irn_publish` reply shows how long the peer took to receive the message. That makes tracing the first place to look when a request seems stuck.

## Storage

All state goes through `storage.KV`, a small transactional key-value interface. Two implementations ship with the SDK:

- `storage.NewMemory()` is the default. State is lost when the process exits.
- `storage.OpenBolt(path)` persists to a single [bbolt](https://github.com/etcd-io/bbolt) file. A client restarted on the same file keeps its identity and sessions, and receives messages the peer sent while it was down.

To use another database, implement `storage.KV`. `Update` must be atomic and durable when it returns.

## Scope

Supported: pairing, session proposal, approval, rejection and settlement, requests and responses, events, update, extend, ping, disconnect, and expiry of pairings, proposals, sessions and requests.

Not supported yet:

- **One-click auth.** This covers `wc_sessionAuthenticate`, SIWE/CACAO and ReCaps. A Go wallet ignores authenticate requests and approves the JS dApp's fallback proposal instead, so the session still works.
- **The Verify API.** Every proposal and request reports `VerificationUnknown`.
- **Link mode, push notifications and pairing reuse.** Each pairing is used for one proposal.

## How it works

- **One goroutine owns the relay WebSocket.** It reconnects with backoff, re-subscribes to every known topic after each connect, and resends requests that were in flight.
- **Acks follow processing.** An incoming message is acked only after its state change is stored. If storage fails, the relay redelivers it. Duplicate deliveries are dropped using the request history.
- **The engine handles incoming messages one at a time** on its own goroutine. API methods run on the caller's goroutine.

## Testing

Unit tests run offline. Tests against the live relay skip locally unless `TEST_PROJECT_ID` is set. In CI (`CI` set) they fail instead of skipping, so a missing secret cannot pass silently; CI allows skipping, with `TEST_ALLOW_SKIP_LIVE`, only for pull requests from forks and Dependabot, which get no secrets:

```sh
TEST_PROJECT_ID=... TEST_RELAY_URL=wss://relay.walletconnect.com go test -race ./...
```

A soak test runs many full session lifecycles in one process and fails if goroutines, heap, engine state or storage do not return to their baseline:

```sh
TEST_SOAK=100 TEST_PROJECT_ID=... go test -run TestE2ESoakNoLeaks -timeout 30m .
```

Interop tests run the published JS SDK against this one, in both roles:

```sh
(cd interop/js && npm ci)
TEST_PROJECT_ID=... go test -tags interop ./interop/
```

A benchmark times connect, sign and disconnect between two clients of the same SDK, for Go and for the published JS SDK, and prints JSON:

```sh
TEST_PROJECT_ID=... go run ./interop/bench -n 30
(cd interop/js && TEST_PROJECT_ID=... node bench.mjs 30)
```

`testdata/vectors.json` holds golden values for the crypto, relay auth, URI and namespace rules, generated from the JS SDK. Regenerate it with `node interop/js/gen-vectors.mjs` after an `npm ci` in `interop/js`.

## Layout

| Path | Contents |
|---|---|
| `client.go`, `dapp.go`, `wallet.go`, `session.go` | Public API: lifecycle, dApp, wallet and shared session calls |
| `events.go`, `types.go`, `errors.go` | Public events, types and errors |
| `storage/` | `KV` interface, memory and bbolt backends |
| `internal/engine` | Sign protocol. `dapp.go`, `wallet_*.go` and `session.go` send; `handle*.go` receive; `expiry.go` sweeps; `waiters.go`, `requests.go` and `publish.go` are the plumbing |
| `internal/relay` | Relay transport. `client.go` is the API, `runner.go` the state machine, `connection.go` the socket, `calls.go` request tracking, `frames.go` frame parsing |
| `internal/store` | Typed records over `storage.KV`, one file per record kind |
| `internal/namespaces` | CAIP-2/10 parsing, namespace validation and conformance |
| `internal/crypto`, `relayauth`, `uri`, `jsonrpc`, `sdkerr`, `queue` | Protocol building blocks |
| `interop/` | JS interop tests and vector generator |
