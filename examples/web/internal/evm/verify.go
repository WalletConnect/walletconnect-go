package evm

import (
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// Recover returns the address that produced signature for the request.
func Recover(method string, params json.RawMessage, signature string) (common.Address, error) {
	hash, _, err := digest(method, params)
	if err != nil {
		return common.Address{}, err
	}
	sig, err := hexutil.Decode(signature)
	if err != nil || len(sig) != 65 {
		return common.Address{}, fmt.Errorf("signature must be 65 hex bytes")
	}
	sig = append([]byte(nil), sig...)
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := crypto.SigToPub(hash.Bytes(), sig)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}
