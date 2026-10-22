// Package namespaces ports the CAIP-25 namespace rules of the JS SDK
// (packages/utils/src/validators.ts and namespaces.ts).

package namespaces

import (
	"slices"
	"strings"
)

type ProposalNamespace struct {
	Chains  []string `json:"chains,omitempty"`
	Methods []string `json:"methods"`
	Events  []string `json:"events"`
}

type SessionNamespace struct {
	Chains   []string `json:"chains,omitempty"`
	Accounts []string `json:"accounts"`
	Methods  []string `json:"methods"`
	Events   []string `json:"events"`
}

type Proposal = map[string]ProposalNamespace

type Session = map[string]SessionNamespace

// NormalizeProposal replaces nil slices with empty ones so they marshal as [].
func NormalizeProposal(ns Proposal) Proposal {
	out := make(Proposal, len(ns))
	for k, v := range ns {
		if v.Methods == nil {
			v.Methods = []string{}
		}
		if v.Events == nil {
			v.Events = []string{}
		}
		out[k] = v
	}
	return out
}

// NormalizeSession replaces nil slices with empty ones so they marshal as [].
func NormalizeSession(ns Session) Session {
	out := make(Session, len(ns))
	for k, v := range ns {
		if v.Accounts == nil {
			v.Accounts = []string{}
		}
		if v.Methods == nil {
			v.Methods = []string{}
		}
		if v.Events == nil {
			v.Events = []string{}
		}
		out[k] = v
	}
	return out
}

func isCaipKey(key string) bool { return strings.Contains(key, ":") }

func namespaceKey(key string) string {
	if k, _, ok := strings.Cut(key, ":"); ok {
		return k
	}
	return key
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
