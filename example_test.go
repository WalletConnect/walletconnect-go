package walletconnect_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	walletconnect "github.com/WalletConnect/walletconnect-go"
)

// A dApp proposes a session, waits for a wallet to approve it and asks for a
// signature.
func Example_dApp() {
	ctx := context.Background()
	client, err := walletconnect.New(ctx, walletconnect.Options{
		ProjectID: "your-project-id",
		Metadata:  walletconnect.Metadata{Name: "My dApp", URL: "https://example.com"},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	conn, err := client.Connect(ctx, walletconnect.ConnectParams{
		OptionalNamespaces: map[string]walletconnect.ProposalNamespace{
			"eip155": {Chains: []string{"eip155:1"}, Methods: []string{"personal_sign"}, Events: []string{"accountsChanged"}},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(conn.URI) // show as a QR code or deep link

	session, err := conn.Wait(ctx)
	if err != nil {
		log.Fatal(err)
	}
	sig, err := client.Request(ctx, walletconnect.RequestParams{
		Topic: session.Topic, ChainID: "eip155:1", Method: "personal_sign",
		Params: json.RawMessage(`["0x68656c6c6f", "0xYourAddress"]`),
	})
	if err != nil {
		log.Fatal(err) // a rejection is an *walletconnect.RPCError
	}
	fmt.Println(string(sig))
}

// A wallet pairs with a dApp's URI, approves its proposal and answers its
// requests.
func Example_wallet() {
	ctx := context.Background()
	client, err := walletconnect.New(ctx, walletconnect.Options{
		ProjectID: "your-project-id",
		Metadata:  walletconnect.Metadata{Name: "My Wallet", URL: "https://wallet.example.com"},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	if err := client.Pair(ctx, "wc:..."); err != nil {
		log.Fatal(err)
	}
	for ev := range client.Events() {
		switch e := ev.(type) {
		case walletconnect.EventSessionProposal:
			_, err := client.Approve(ctx, walletconnect.ApproveParams{
				ProposalID: e.Proposal.ID,
				Namespaces: map[string]walletconnect.SessionNamespace{
					"eip155": {Accounts: []string{"eip155:1:0xYourAddress"}, Methods: []string{"personal_sign"}, Events: []string{"accountsChanged"}},
				},
			})
			if err != nil {
				log.Print(err)
			}
		case walletconnect.EventSessionRequest:
			if e.Method != "personal_sign" {
				_ = client.RespondError(ctx, e.Topic, e.ID, walletconnect.SDKError(walletconnect.ErrKeyUnsupportedMethods, e.Method))
				continue
			}
			_ = client.Respond(ctx, e.Topic, e.ID, "0xsignature")
		}
	}
}
