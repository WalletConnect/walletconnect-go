// Package evm signs and verifies the EVM requests the demo apps exchange:
// personal_sign, eth_sign and eth_signTypedData_v4.
package evm

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// ErrUnsupported is returned for methods the demo wallet cannot handle.
var ErrUnsupported = errors.New("unsupported method")

// SignedMethods are the methods the demo wallet signs.
var SignedMethods = []string{"personal_sign", "eth_sign", "eth_signTypedData", "eth_signTypedData_v4"}

// digest returns the hash a wallet signs for method and the address the
// request names.
func digest(method string, params json.RawMessage) (common.Hash, common.Address, error) {
	if !slices.Contains(SignedMethods, method) {
		return common.Hash{}, common.Address{}, ErrUnsupported
	}
	var args []json.RawMessage
	if err := json.Unmarshal(params, &args); err != nil || len(args) < 2 {
		return common.Hash{}, common.Address{}, fmt.Errorf("%s: params must be a two-item array", method)
	}
	switch method {
	case "personal_sign":
		// [message, address]; some dApps swap the order, as MetaMask tolerates.
		msg, addr := args[0], args[1]
		if isAddress(msg) && !isAddress(addr) {
			msg, addr = addr, msg
		}
		return messageDigest(msg, addr)
	case "eth_sign":
		return messageDigest(args[1], args[0])
	case "eth_signTypedData", "eth_signTypedData_v4":
		var addr string
		if err := json.Unmarshal(args[0], &addr); err != nil {
			return common.Hash{}, common.Address{}, err
		}
		raw := args[1]
		var asString string
		if json.Unmarshal(raw, &asString) == nil {
			raw = json.RawMessage(asString)
		}
		var td apitypes.TypedData
		if err := json.Unmarshal(raw, &td); err != nil {
			return common.Hash{}, common.Address{}, fmt.Errorf("typed data: %w", err)
		}
		hash, _, err := apitypes.TypedDataAndHash(td)
		if err != nil {
			return common.Hash{}, common.Address{}, fmt.Errorf("typed data: %w", err)
		}
		return common.BytesToHash(hash), common.HexToAddress(addr), nil
	default:
		return common.Hash{}, common.Address{}, ErrUnsupported
	}
}

func messageDigest(msgParam, addrParam json.RawMessage) (common.Hash, common.Address, error) {
	var msg, addr string
	if err := json.Unmarshal(msgParam, &msg); err != nil {
		return common.Hash{}, common.Address{}, err
	}
	if err := json.Unmarshal(addrParam, &addr); err != nil {
		return common.Hash{}, common.Address{}, err
	}
	data := []byte(msg)
	if b, err := hexutil.Decode(msg); err == nil {
		data = b
	}
	return common.BytesToHash(accounts.TextHash(data)), common.HexToAddress(addr), nil
}

func isAddress(raw json.RawMessage) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil && common.IsHexAddress(s) && len(s) == 42
}

// ChainNumber turns "eip155:10" into 10.
func ChainNumber(caip2 string) (int64, error) {
	ns, ref, ok := strings.Cut(caip2, ":")
	if !ok || ns != "eip155" {
		return 0, fmt.Errorf("not an eip155 chain: %q", caip2)
	}
	return strconv.ParseInt(ref, 10, 64)
}

// AccountAddress turns "eip155:1:0xabc…" into 0xabc….
func AccountAddress(caip10 string) string {
	return caip10[strings.LastIndex(caip10, ":")+1:]
}
