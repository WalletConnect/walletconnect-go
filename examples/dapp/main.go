// Command dapp proposes a session, prints the pairing URI for a wallet to
// scan, then asks the wallet to sign a message.
//
//	WC_PROJECT_ID=... go run ./examples/dapp
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	wc "github.com/WalletConnect/walletconnect-go"
	"github.com/WalletConnect/walletconnect-go/storage"
)

func main() {
	relayURL := flag.String("relay", wc.DefaultRelayURL, "relay URL")
	dbPath := flag.String("db", "", "bbolt file to persist sessions in (default: in memory)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var kv storage.KV = storage.NewMemory()
	if *dbPath != "" {
		db, err := storage.OpenBolt(*dbPath)
		if err != nil {
			log.Fatal(err)
		}
		defer db.Close()
		kv = db
	}
	client, err := wc.New(ctx, wc.Options{
		ProjectID: os.Getenv("WC_PROJECT_ID"),
		RelayURL:  *relayURL,
		Storage:   kv,
		Metadata: wc.Metadata{
			Name: "Go example dApp", Description: "walletconnect-go example",
			URL: "https://example.com", Icons: []string{"https://avatars.githubusercontent.com/u/37784886"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	conn, err := client.Connect(ctx, wc.ConnectParams{OptionalNamespaces: map[string]wc.ProposalNamespace{
		"eip155": {Chains: []string{"eip155:1"}, Methods: []string{"personal_sign"}, Events: []string{"accountsChanged", "chainChanged"}},
	}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Pair a wallet with this URI:\n\n%s\n\n", conn.URI)

	session, err := conn.Wait(ctx)
	if err != nil {
		log.Fatalf("session not established: %v", err)
	}
	account := session.Namespaces["eip155"].Accounts[0]
	fmt.Printf("Connected to %s as %s\n", session.Peer.Metadata.Name, account)

	go func() {
		for ev := range client.Events() {
			switch e := ev.(type) {
			case wc.EventSessionEvent:
				fmt.Printf("wallet event %s on %s: %s\n", e.Name, e.ChainID, e.Data)
			case wc.EventSessionDelete:
				fmt.Println("wallet disconnected")
				stop()
			}
		}
	}()

	address := account[strings.LastIndex(account, ":")+1:]
	params, _ := json.Marshal([]string{"0x" + fmt.Sprintf("%x", "hello from Go"), address})
	sig, err := client.Request(ctx, wc.RequestParams{Topic: session.Topic, ChainID: "eip155:1", Method: "personal_sign", Params: params})
	if err != nil {
		log.Printf("personal_sign failed: %v", err)
	} else {
		fmt.Printf("signature: %s\n", sig)
	}

	fmt.Println("Press Ctrl-C to disconnect.")
	<-ctx.Done()
	if err := client.Disconnect(context.Background(), session.Topic); err != nil {
		log.Printf("disconnect: %v", err)
	}
}
