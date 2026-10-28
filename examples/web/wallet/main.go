// Command wallet is a demo web wallet running on the Go SDK. It pairs from a
// wc: URI, approves or rejects proposals, and signs requests with a local test
// key kept in its data directory. Never send real funds to that key.
//
//	WC_PROJECT_ID=... go run ./wallet
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"path/filepath"

	wc "github.com/WalletConnect/walletconnect-go"
	"github.com/WalletConnect/walletconnect-go/examples/web/internal/evm"
	"github.com/WalletConnect/walletconnect-go/examples/web/internal/server"
)

//go:embed static
var static embed.FS

func main() {
	addr := flag.String("addr", ":8081", "HTTP listen address")
	dataDir := flag.String("data", "data/wallet", "directory for the session store and test key")
	relayURL := flag.String("relay", wc.DefaultRelayURL, "relay URL")
	debug := flag.Bool("debug", false, "trace every action and relay message from start to finish")
	publicURL := flag.String("url", "", "public URL sent to peers in the metadata (default http://localhost<addr>)")
	flag.Parse()
	if *publicURL == "" {
		*publicURL = "http://localhost" + *addr
	}

	app, closeApp, err := server.Start(server.Config{
		Name: "wallet", DataDir: *dataDir, RelayURL: *relayURL, Debug: *debug,
		Metadata: wc.Metadata{
			Name: "Go demo wallet", Description: "walletconnect-go web demo",
			URL: *publicURL, Icons: []string{"https://avatars.githubusercontent.com/u/37784886"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer closeApp()
	signer, err := evm.LoadOrCreate(filepath.Join(*dataDir, "key.hex"))
	if err != nil {
		log.Fatal(err)
	}
	app.Log.Info("test account", "address", signer.Address.Hex())

	page, _ := fs.Sub(static, "static")
	srv := server.New(page, app.Log)
	srv.ServeTrace(app.TraceFile)
	registerAPI(srv, app.Client, signer)
	go srv.PumpEvents(app.Ctx, app.Client.Events())
	if err := server.Serve(app, *addr, srv); err != nil {
		log.Fatal(err)
	}
}
