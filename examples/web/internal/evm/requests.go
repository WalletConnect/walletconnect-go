package evm

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

// RequestParams builds demo params for method, signed by address on chain.
func RequestParams(method, address string, chain int64) (json.RawMessage, error) {
	switch method {
	case "personal_sign":
		msg := fmt.Sprintf("Hello from the Go dApp at %s", time.Now().Format(time.RFC3339))
		return json.Marshal([]string{hexutil.Encode([]byte(msg)), address})
	case "eth_signTypedData_v4":
		td, err := json.Marshal(sampleTypedData(chain, address))
		if err != nil {
			return nil, err
		}
		return json.Marshal([]string{address, string(td)})
	case "eth_sendTransaction":
		// A zero-value transfer to yourself: a real wallet still charges gas.
		return json.Marshal([]map[string]string{{"from": address, "to": address, "value": "0x0", "data": "0x"}})
	default:
		return nil, fmt.Errorf("the demo cannot build params for %s", method)
	}
}

// sampleTypedData is the EIP-712 "Mail" example bound to chain.
func sampleTypedData(chain int64, from string) map[string]any {
	return map[string]any{
		"types": map[string]any{
			"EIP712Domain": []map[string]string{
				{"name": "name", "type": "string"},
				{"name": "version", "type": "string"},
				{"name": "chainId", "type": "uint256"},
				{"name": "verifyingContract", "type": "address"},
			},
			"Person": []map[string]string{{"name": "name", "type": "string"}, {"name": "wallet", "type": "address"}},
			"Mail": []map[string]string{
				{"name": "from", "type": "Person"}, {"name": "to", "type": "Person"}, {"name": "contents", "type": "string"},
			},
		},
		"primaryType": "Mail",
		"domain": map[string]any{
			"name": "walletconnect-go demo", "version": "1", "chainId": chain,
			"verifyingContract": "0xCcCCccccCCCCcCCCCCCcCcCccCcCCCcCcccccccC",
		},
		"message": map[string]any{
			"from":     map[string]string{"name": "Go dApp", "wallet": from},
			"to":       map[string]string{"name": "Bob", "wallet": "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB"},
			"contents": "Hello, Bob!",
		},
	}
}
