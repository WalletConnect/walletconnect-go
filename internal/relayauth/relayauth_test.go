package relayauth

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"

	"github.com/WalletConnect/walletconnect-go/internal/crypto"
	"github.com/WalletConnect/walletconnect-go/internal/testvectors"
)

func TestVectors(t *testing.T) {
	v := testvectors.Load(t).Auth
	seed, _ := hex.DecodeString(v.Seed)
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	if hex.EncodeToString(pub) != v.PublicKey {
		t.Fatalf("public key = %x, want %s", pub, v.PublicKey)
	}
	if got := ClientID(pub); got != v.ClientID {
		t.Fatalf("client id = %s, want %s", got, v.ClientID)
	}
	jwt, err := SignJWT(priv, v.Sub, v.Aud, v.Iat, v.TTL)
	if err != nil {
		t.Fatal(err)
	}
	if jwt != v.JWT {
		t.Fatalf("jwt =\n%s\nwant\n%s", jwt, v.JWT)
	}
	if got := crypto.HashMessage(v.SubscriptionTopic + v.ClientID); got != v.SubscriptionID {
		t.Fatalf("subscription id = %s, want %s", got, v.SubscriptionID)
	}
}
