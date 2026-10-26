package engine

// Transaction value flow (TVF) data is what the relay collects about the
// messages it carries: which request a message belongs to, what it does on
// which chain, which session was approved, and the transaction hashes a
// result produced. It rides on the relay call's params next to topic,
// message, ttl and tag; the peer never sees it, since it is outside the
// encrypted message. The fields and where they attach mirror getTVFParams,
// getTVFApproveParams, sendRequest and sendResult in
// packages/sign-client/src/controllers/engine.ts.
//
// Everything here is best effort: a value that cannot be derived is left
// out, and nothing stops a publish.

import (
	"encoding/json"
	"slices"
	"sort"

	"github.com/WalletConnect/walletconnect-go/internal/namespaces"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

type tvf struct {
	correlationID     int64
	rpcMethods        []string
	chainID           string
	txHashes          []string
	contractAddresses []string
	approvedChains    []string
	approvedMethods   []string
	approvedEvents    []string
	sessionProperties map[string]string
	scopedProperties  map[string]json.RawMessage
}

// addTo puts the set fields on a relay call's params.
func (t *tvf) addTo(p map[string]any) {
	if t == nil {
		return
	}
	if t.correlationID != 0 {
		p["correlationId"] = t.correlationID
	}
	if t.chainID != "" {
		p["chainId"] = t.chainID
	}
	for key, v := range map[string][]string{
		"rpcMethods": t.rpcMethods, "txHashes": t.txHashes, "contractAddresses": t.contractAddresses,
		"approvedChains": t.approvedChains, "approvedMethods": t.approvedMethods, "approvedEvents": t.approvedEvents,
	} {
		if len(v) > 0 {
			p[key] = v
		}
	}
	if len(t.sessionProperties) > 0 {
		p["sessionProperties"] = t.sessionProperties
	}
	if len(t.scopedProperties) > 0 {
		p["scopedProperties"] = t.scopedProperties
	}
}

// correlate is what every request and every result carries: the payload id
// of the request, so the relay can pair a request with its answer.
func correlate(id int64) *tvf { return &tvf{correlationID: id} }

// tvfForRequest describes an outgoing session request: the chain, the
// method and, when the call carries calldata, the contract it targets.
func tvfForRequest(id int64, rp requestParams) *tvf {
	return &tvf{correlationID: id, rpcMethods: []string{rp.Request.Method}, chainID: rp.ChainID,
		contractAddresses: contractAddresses(rp.Request.Params)}
}

// tvfForResult describes a wallet's result for a session request, with the
// transaction hashes the result carries or that follow from it.
func tvfForResult(id int64, req *types.PendingRequest, result json.RawMessage) *tvf {
	return &tvf{correlationID: id, rpcMethods: []string{req.Method}, chainID: req.ChainID,
		txHashes: txHashes(req.Method, req.Params, result), contractAddresses: contractAddresses(req.Params)}
}

// tvfForApprove describes a settled session: the chains, methods and
// events it allows and its properties, under the proposal's id.
func tvfForApprove(proposalID int64, s types.Session) *tvf {
	return &tvf{correlationID: proposalID, approvedChains: namespaces.Chains(s.Namespaces),
		approvedMethods:   namespaceValues(s.Namespaces, func(ns types.SessionNamespace) []string { return ns.Methods }),
		approvedEvents:    namespaceValues(s.Namespaces, func(ns types.SessionNamespace) []string { return ns.Events }),
		sessionProperties: s.SessionProperties, scopedProperties: s.ScopedProperties}
}

// namespaceValues flattens one list of every namespace, in key order,
// without duplicates.
func namespaceValues(ns map[string]types.SessionNamespace, pick func(types.SessionNamespace) []string) []string {
	keys := make([]string, 0, len(ns))
	for k := range ns {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		for _, v := range pick(ns[k]) {
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	return out
}

// contractAddresses is the target of an EVM call, when the request's first
// parameter is a transaction object whose data is hex calldata: the shape
// of eth_sendTransaction and wallet_sendCalls. A bare "0x" counts, as it
// does in the JS client.
func contractAddresses(params json.RawMessage) []string {
	var list []json.RawMessage
	first := params
	if err := json.Unmarshal(params, &list); err == nil {
		if len(list) == 0 {
			return nil
		}
		first = list[0]
	}
	var call struct {
		Data json.RawMessage `json:"data"`
		To   string          `json:"to"`
	}
	if err := json.Unmarshal(first, &call); err != nil {
		return nil
	}
	var data string
	if err := json.Unmarshal(call.Data, &data); err != nil || !isHexCalldata(data) || call.To == "" {
		return nil
	}
	return []string{call.To}
}

func isHexCalldata(s string) bool {
	if len(s) < 2 || s[:2] != "0x" || len(s)%2 != 0 {
		return false
	}
	for _, c := range s[2:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
