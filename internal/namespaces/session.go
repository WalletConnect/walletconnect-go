package namespaces

import "slices"

// Chains returns the unique chains of all accounts in the session namespaces.
func Chains(ns Session) []string {
	var out []string
	for _, key := range sortedKeys(ns) {
		for _, c := range AccountsChains(ns[key].Accounts) {
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	return out
}

func MethodsForChain(ns Session, chainID string) []string {
	var out []string
	for _, v := range ns {
		if slices.Contains(AccountsChains(v.Accounts), chainID) {
			out = append(out, v.Methods...)
		}
	}
	return out
}

func EventsForChain(ns Session, chainID string) []string {
	var out []string
	for _, v := range ns {
		if slices.Contains(AccountsChains(v.Accounts), chainID) {
			out = append(out, v.Events...)
		}
	}
	return out
}

func IsValidChainForSession(ns Session, chainID string) bool {
	return IsValidChainID(chainID) && slices.Contains(Chains(ns), chainID)
}

func IsValidMethodForSession(ns Session, chainID, method string) bool {
	return method != "" && slices.Contains(MethodsForChain(ns, chainID), method)
}

func IsValidEventForSession(ns Session, chainID, name string) bool {
	return name != "" && slices.Contains(EventsForChain(ns, chainID), name)
}
