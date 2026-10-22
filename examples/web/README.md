# Web demo: dApp and wallet on the Go SDK

Two small web apps for trying walletconnect-go by hand. Each is a Go HTTP server running the SDK, with a plain HTML page that talks to it over JSON calls and server-sent events.

| App | Default URL | What it does |
|---|---|---|
| `dapp` | http://localhost:8080 | Proposes a session with a QR code, sends `personal_sign`, `eth_signTypedData_v4` and `eth_sendTransaction`, and checks that each signature recovers to the session account |
| `wallet` | http://localhost:8081 | Pairs from a `wc:` URI, approves or rejects proposals, signs `personal_sign`, `eth_sign` and `eth_signTypedData_v4` with a local test key, and can emit events, add chains, extend, ping and disconnect |

They work with each other and with other WalletConnect apps: scan the dApp's QR code with a mobile wallet, or paste a URI from any web dApp into the Go wallet.

## Run

```sh
cd examples/web
export WC_PROJECT_ID=...   # from the WalletConnect dashboard
go run ./dapp              # http://localhost:8080
go run ./wallet            # http://localhost:8081, in another terminal
```

Flags: `-addr` for the listen address, `-data` for the state directory, and `-relay` for the relay URL.

## State and restarts

Each app keeps its sessions in `data/<app>/walletconnect.db`, a bbolt file. Stop and start an app to test resuming. The wallet also keeps its test private key in `data/wallet/key.hex`. It is a throwaway key for testing; never send funds to it. Delete the `data` directory to start fresh.

## Not covered

The wallet holds one EVM account and answers `eth_sendTransaction` with an unsupported-method error, since it has no chain RPC. Non-EVM namespaces are ignored, and a proposal that requires one cannot be approved. Every proposal and request shows verification `UNKNOWN`, because the SDK does not implement the Verify API.
