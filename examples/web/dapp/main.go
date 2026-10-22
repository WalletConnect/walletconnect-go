// Command dapp is a demo web dApp running on the Go SDK. It proposes sessions
// with a QR code, sends signing requests and verifies the signatures.
//
//	WC_PROJECT_ID=... go run ./dapp
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"

	wc "github.com/WalletConnect/walletconnect-go"
	"github.com/WalletConnect/walletconnect-go/examples/web/internal/server"
)

//go:embed static
var static embed.FS

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dataDir := flag.String("data", "data/dapp", "directory for the session store")
	relayURL := flag.String("relay", wc.DefaultRelayURL, "relay URL")
	debug := flag.Bool("debug", false, "trace every action and relay message from start to finish")
	publicURL := flag.String("url", "", "public URL sent to peers in the metadata (default http://localhost<addr>)")
	flag.Parse()
	if *publicURL == "" {
		*publicURL = "http://localhost" + *addr
	}

	app, closeApp, err := server.Start(server.Config{
		Name: "dapp", DataDir: *dataDir, RelayURL: *relayURL, Debug: *debug,
		Metadata: wc.Metadata{
			Name: "Go demo dApp", Description: "walletconnect-go web demo",
			URL: *publicURL, Icons: []string{"https://avatars.githubusercontent.com/u/37784886"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer closeApp()

	page, _ := fs.Sub(static, "static")
	srv := server.New(page, app.Log)
	registerAPI(srv, app.Client)
	go srv.PumpEvents(app.Ctx, app.Client.Events())
	if err := server.Serve(app, *addr, srv); err != nil {
		log.Fatal(err)
	}
}
