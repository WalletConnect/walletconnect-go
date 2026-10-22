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

Flags: `-addr` for the listen address, `-data` for the state directory, `-relay` for the relay URL, and `-url` for the public URL sent to peers in the app metadata.

## State and restarts

Each app keeps its sessions in `data/<app>/walletconnect.db`, a bbolt file. Stop and start an app to test resuming. The wallet also keeps its test private key in `data/wallet/key.hex`. It is a throwaway key for testing; never send funds to it. Delete the `data` directory to start fresh.

## Deploy to Cloud Run

`Dockerfile` builds both apps into one image. It builds from the repository root so the module's `replace` directive finds the SDK, and Cloud Build runs it, so you don't need Docker locally. Each app runs as its own Cloud Run service from that image.

One-time setup, in a Google Cloud project with billing enabled:

```sh
gcloud services enable run.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com
gcloud artifacts repositories create web-demo --repository-format=docker --location=europe-west1
```

Build the image from the repository root, then deploy each app. Service URLs have the form `https://<service>-<project number>.<region>.run.app`, so `-url` can be set before the first deploy:

```sh
PROJECT=$(gcloud config get project)
NUMBER=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')
IMAGE=europe-west1-docker.pkg.dev/$PROJECT/web-demo/web:latest

gcloud builds submit --region europe-west1 --config examples/web/cloudbuild.yaml --substitutions _IMAGE=$IMAGE .

for app in dapp wallet; do
  gcloud run deploy wc-$app --region europe-west1 --image $IMAGE --command /app/wc-$app \
    --args="-addr=:8080,-data=/tmp/wc-$app,-url=https://wc-$app-$NUMBER.europe-west1.run.app" \
    --set-env-vars WC_PROJECT_ID=... \
    --max-instances 1 --timeout 3600 --cpu 1 --memory 256Mi --allow-unauthenticated
done
```

To ship a change, rebuild the image and run `gcloud run services update wc-<app> --region europe-west1 --image $IMAGE` for each app.

Things to know:

- Each app keeps its state in memory, so it is capped at one instance.
- An open page holds a request, which keeps the instance running and connected to the relay. With no page open, Cloud Run stops the instance, and the next visit starts with no sessions and a new wallet key.
- The one-hour timeout bounds the event stream. The page reconnects when it ends.
- The wallet has no auth. Anyone with its URL can approve sessions and sign with its key.

## Not covered

The wallet holds one EVM account and answers `eth_sendTransaction` with an unsupported-method error, since it has no chain RPC. Non-EVM namespaces are ignored, and a proposal that requires one cannot be approved. Every proposal and request shows verification `UNKNOWN`, because the SDK does not implement the Verify API.
