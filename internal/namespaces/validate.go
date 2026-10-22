package namespaces

import (
	"fmt"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/sdkerr"
)

// ValidateProposal ports isValidRequiredNamespaces.
func ValidateProposal(ns Proposal, method, kind string) *jsonrpc.Error {
	if len(ns) == 0 {
		return sdkerr.New(sdkerr.MissingOrInvalid, fmt.Sprintf("%s, %s should be an object with data", method, kind))
	}
	var err *jsonrpc.Error
	for _, key := range sortedKeys(ns) {
		v := ns[key]
		if e := validActions(v.Methods, v.Events, method+", namespace"); e != nil && err == nil {
			err = e
		}
	}
	for _, key := range sortedKeys(ns) {
		if e := validChains(key, ProposalChains(key, ns[key]), method+" "+kind); e != nil {
			return e
		}
	}
	return err
}

// ValidateSession ports isValidNamespaces.
func ValidateSession(ns Session, method string) *jsonrpc.Error {
	if len(ns) == 0 {
		return sdkerr.New(sdkerr.MissingOrInvalid, method+", namespaces should be an object with data")
	}
	var err *jsonrpc.Error
	for _, key := range sortedKeys(ns) {
		v := ns[key]
		if e := validActions(v.Methods, v.Events, method+", namespace"); e != nil {
			err = e
			break
		}
	}
	for _, key := range sortedKeys(ns) {
		accounts := ns[key].Accounts
		if accounts == nil {
			return sdkerr.New(sdkerr.UnsupportedAccounts, method+" namespace, accounts should be an array of strings conforming to \"namespace:chainId:address\" format")
		}
		for _, a := range accounts {
			if !IsValidAccountID(a) {
				return sdkerr.New(sdkerr.UnsupportedAccounts, fmt.Sprintf("%s namespace, account %s should be a string and conform to \"namespace:chainId:address\" format", method, a))
			}
		}
	}
	return err
}

// ProposalChains returns [key] for an inline chain key, else ns.Chains.
func ProposalChains(key string, ns ProposalNamespace) []string {
	if isCaipKey(key) {
		return []string{key}
	}
	return ns.Chains
}

func validActions(methods, events []string, context string) *jsonrpc.Error {
	if !validStrings(methods) {
		return sdkerr.New(sdkerr.UnsupportedMethods, context+", methods should be an array of strings or empty array for no methods")
	}
	if !validStrings(events) {
		return sdkerr.New(sdkerr.UnsupportedEvents, context+", events should be an array of strings or empty array for no events")
	}
	return nil
}

func validChains(key string, chains []string, context string) *jsonrpc.Error {
	if len(chains) > 0 {
		for _, c := range chains {
			if !IsValidChainID(c) {
				return sdkerr.New(sdkerr.UnsupportedChains, fmt.Sprintf("%s, chain %s should be a string and conform to \"namespace:chainId\" format", context, c))
			}
		}
		return nil
	}
	if !IsValidChainID(key) {
		return sdkerr.New(sdkerr.UnsupportedChains, context+", chains must be defined as \"namespace:chainId\" e.g. \"eip155:1\": {...} in the namespace key OR as an array of CAIP-2 chainIds e.g. eip155: { chains: [\"eip155:1\", \"eip155:5\"] }")
	}
	return nil
}

func validStrings(xs []string) bool {
	if xs == nil {
		return false
	}
	for _, x := range xs {
		if x == "" {
			return false
		}
	}
	return true
}
