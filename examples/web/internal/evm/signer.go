package evm

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// Signer holds the demo wallet's test key. Never use it with real funds.
type Signer struct {
	key     *ecdsa.PrivateKey
	Address common.Address
}

// LoadOrCreate reads a hex private key from path, creating one if missing.
func LoadOrCreate(path string) (*Signer, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key, err := crypto.GenerateKey()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(hexutil.Encode(crypto.FromECDSA(key))), 0o600); err != nil {
			return nil, err
		}
		return &Signer{key: key, Address: crypto.PubkeyToAddress(key.PublicKey)}, nil
	}
	if err != nil {
		return nil, err
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(strings.TrimSpace(string(b)), "0x"))
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", path, err)
	}
	return &Signer{key: key, Address: crypto.PubkeyToAddress(key.PublicKey)}, nil
}

// Sign answers a signing request. It returns ErrUnsupported for methods it
// does not sign, and an error if the request names another address.
func (s *Signer) Sign(method string, params json.RawMessage) (string, error) {
	hash, addr, err := digest(method, params)
	if err != nil {
		return "", err
	}
	if addr != s.Address {
		return "", fmt.Errorf("request is for %s, wallet holds %s", addr.Hex(), s.Address.Hex())
	}
	sig, err := crypto.Sign(hash.Bytes(), s.key)
	if err != nil {
		return "", err
	}
	sig[64] += 27
	return hexutil.Encode(sig), nil
}
