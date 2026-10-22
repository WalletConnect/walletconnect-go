// Package relayauth builds the did:key client ID and the EdDSA JWT the relay
// expects in the auth query parameter.
package relayauth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"

	"github.com/mr-tron/base58"
)

var multicodecEd25519 = []byte{0xed, 0x01}

// ClientID returns did:key:z<base58btc(0xed01 || pub)>.
func ClientID(pub ed25519.PublicKey) string {
	b := make([]byte, 0, len(multicodecEd25519)+len(pub))
	b = append(b, multicodecEd25519...)
	b = append(b, pub...)
	return "did:key:z" + base58.Encode(b)
}

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type claims struct {
	Iss string `json:"iss"`
	Sub string `json:"sub"`
	Aud string `json:"aud"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// SignJWT returns header.payload.signature, each segment unpadded base64url.
func SignJWT(priv ed25519.PrivateKey, sub, aud string, iat, ttl int64) (string, error) {
	h, err := json.Marshal(header{Alg: "EdDSA", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	pub := priv.Public().(ed25519.PublicKey)
	p, err := json.Marshal(claims{Iss: ClientID(pub), Sub: sub, Aud: aud, Iat: iat, Exp: iat + ttl})
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(h) + "." + enc.EncodeToString(p)
	sig := ed25519.Sign(priv, []byte(signingInput))
	return signingInput + "." + enc.EncodeToString(sig), nil
}
