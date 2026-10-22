// Command wallet pairs with a dApp from a wc: URI, approves its proposal for
// one EVM address, and answers session requests. It holds no keys, so it
// rejects every request; replace answer with real signing.
//
//	WC_PROJECT_ID=... go run ./examples/wallet -uri 'wc:...' -address 0x...
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	wc "github.com/WalletConnect/walletconnect-go"
	"github.com/WalletConnect/walletconnect-go/storage"
)

func main() {
	uri := flag.String("uri", "", "wc: pairing URI from the dApp")
	address := flag.String("address", "0xab16a96D359eC26a11e2C2b3d8f8B8942d5Bfcdb", "account address to expose")
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
		Metadata:  wc.Metadata{Name: "Go example wallet", Description: "walletconnect-go example", URL: "https://example.com"},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	if *uri != "" {
		if err := client.Pair(ctx, *uri); err != nil {
			log.Fatalf("pair: %v", err)
		}
	}
	fmt.Println("Waiting for proposals and requests. Press Ctrl-C to exit.")

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-client.Events():
			switch e := ev.(type) {
			case wc.EventSessionProposal:
				approve(ctx, client, e.Proposal, *address)
			case wc.EventSessionRequest:
				answer(ctx, client, e)
			case wc.EventSessionDelete:
				fmt.Printf("session %s deleted by the dApp\n", e.Topic)
			case wc.EventRelayState:
				if e.Err != nil {
					log.Printf("relay: %v", e.Err)
				}
			}
		}
	}
}

// approve grants every requested eip155 chain, method and event for address.
func approve(ctx context.Context, client *wc.Client, p wc.Proposal, address string) {
	fmt.Printf("Proposal from %s (%s)\n", p.Proposer.Metadata.Name, p.Proposer.Metadata.URL)
	ns := wc.SessionNamespace{}
	for _, set := range []map[string]wc.ProposalNamespace{p.RequiredNamespaces, p.OptionalNamespaces} {
		for key, req := range set {
			chains := req.Chains
			if key != "eip155" {
				if len(key) > 7 && key[:7] == "eip155:" {
					chains = []string{key}
				} else {
					continue
				}
			}
			for _, c := range chains {
				ns.Chains = append(ns.Chains, c)
				ns.Accounts = append(ns.Accounts, c+":"+address)
			}
			ns.Methods = append(ns.Methods, req.Methods...)
			ns.Events = append(ns.Events, req.Events...)
		}
	}
	if len(ns.Accounts) == 0 {
		fmt.Println("No eip155 chains requested; rejecting.")
		if err := client.Reject(ctx, p.ID, wc.SDKError(wc.ErrKeyUnsupportedChains, "")); err != nil {
			log.Printf("reject: %v", err)
		}
		return
	}
	approval, err := client.Approve(ctx, wc.ApproveParams{ProposalID: p.ID, Namespaces: map[string]wc.SessionNamespace{"eip155": ns}})
	if err != nil {
		log.Printf("approve: %v", err)
		return
	}
	if err := approval.Acknowledged(ctx); err != nil {
		log.Printf("dApp did not acknowledge: %v", err)
		return
	}
	fmt.Printf("Session %s established\n", approval.Session.Topic)
}

func answer(ctx context.Context, client *wc.Client, r wc.EventSessionRequest) {
	params, _ := json.Marshal(r.Params)
	fmt.Printf("Request %d: %s on %s %s\n", r.ID, r.Method, r.ChainID, params)
	if err := client.RespondError(ctx, r.Topic, r.ID, wc.SDKError(wc.ErrKeyUserRejected, "example wallet holds no keys")); err != nil {
		log.Printf("respond: %v", err)
	}
}
