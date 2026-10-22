package main

import (
	"fmt"
	"slices"
	"strings"

	wc "github.com/WalletConnect/walletconnect-go"
	"github.com/WalletConnect/walletconnect-go/examples/web/internal/evm"
)

// approvedNamespaces grants every eip155 chain, method and event the
// proposal asks for, for the wallet's one address, plus the methods the
// wallet signs. A proposal with no namespaces gets Ethereum mainnet.
func approvedNamespaces(p wc.Proposal, address string) (map[string]wc.SessionNamespace, error) {
	var chains, methods, events []string
	add := func(list *[]string, items ...string) {
		for _, it := range items {
			if !slices.Contains(*list, it) {
				*list = append(*list, it)
			}
		}
	}
	for _, set := range []map[string]wc.ProposalNamespace{p.RequiredNamespaces, p.OptionalNamespaces} {
		for key, ns := range set {
			namespace, _, inline := strings.Cut(key, ":")
			if namespace != "eip155" {
				if _, required := p.RequiredNamespaces[key]; required {
					return nil, fmt.Errorf("%w: the demo wallet only supports eip155, %q is required", wc.ErrInvalidParams, key)
				}
				continue
			}
			if inline {
				add(&chains, key)
			}
			add(&chains, ns.Chains...)
			add(&methods, ns.Methods...)
			add(&events, ns.Events...)
		}
	}
	if len(chains) == 0 {
		chains = []string{"eip155:1"}
	}
	add(&methods, evm.SignedMethods...)
	add(&events, "chainChanged", "accountsChanged")
	accounts := make([]string, len(chains))
	for i, c := range chains {
		accounts[i] = c + ":" + address
	}
	return map[string]wc.SessionNamespace{"eip155": {Chains: chains, Accounts: accounts, Methods: methods, Events: events}}, nil
}

// withChain returns the session's namespaces with chain added.
func withChain(s wc.Session, chain, address string) map[string]wc.SessionNamespace {
	out := map[string]wc.SessionNamespace{}
	for k, ns := range s.Namespaces {
		out[k] = ns
	}
	ns := out["eip155"]
	if !slices.Contains(ns.Chains, chain) {
		ns.Chains = append(slices.Clone(ns.Chains), chain)
		ns.Accounts = append(slices.Clone(ns.Accounts), chain+":"+address)
	}
	out["eip155"] = ns
	return out
}
