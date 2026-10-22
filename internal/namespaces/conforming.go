package namespaces

import (
	"fmt"
	"slices"
	"strings"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/sdkerr"
)

// Conforming ports isConformingNamespaces. Every required namespace, chain,
// method and event must be covered by the approved namespaces.
func Conforming(required Proposal, approved Session, context string) *jsonrpc.Error {
	var err *jsonrpc.Error
	parsedRequired := parseRequired(required)
	parsedApproved := parseApproved(approved)
	requiredChains := sortedKeys(parsedRequired)
	approvedChains := sortedKeys(parsedApproved)

	uniqueApproved := uniqueNamespaceKeys(sortedKeys(approved))
	var missing []string
	for _, k := range uniqueNamespaceKeys(sortedKeys(required)) {
		if !slices.Contains(uniqueApproved, k) {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		err = sdkerr.New(sdkerr.NonConformingNamespaces, fmt.Sprintf("%s namespaces keys don't satisfy requiredNamespaces. Required: %s Received: %s", context, strings.Join(missing, ","), strings.Join(sortedKeys(approved), ",")))
	}
	if !subset(requiredChains, approvedChains) {
		err = sdkerr.New(sdkerr.NonConformingNamespaces, fmt.Sprintf("%s namespaces chains don't satisfy required namespaces. Required: %s Approved: %s", context, strings.Join(requiredChains, ","), strings.Join(approvedChains, ",")))
	}
	for _, key := range sortedKeys(approved) {
		if err != nil {
			break
		}
		if !isCaipKey(key) {
			continue
		}
		if !slices.Contains(AccountsChains(approved[key].Accounts), key) {
			err = sdkerr.New(sdkerr.NonConformingNamespaces, fmt.Sprintf("%s namespaces accounts don't satisfy namespace accounts for %s", context, key))
		}
	}
	for _, chain := range requiredChains {
		if err != nil {
			break
		}
		r, a := parsedRequired[chain], parsedApproved[chain]
		if !subset(r.methods, a.methods) {
			err = sdkerr.New(sdkerr.NonConformingNamespaces, fmt.Sprintf("%s namespaces methods don't satisfy namespace methods for %s", context, chain))
		} else if !subset(r.events, a.events) {
			err = sdkerr.New(sdkerr.NonConformingNamespaces, fmt.Sprintf("%s namespaces events don't satisfy namespace events for %s", context, chain))
		}
	}
	return err
}

type actions struct{ methods, events []string }

func parseRequired(ns Proposal) map[string]actions {
	out := map[string]actions{}
	for key, v := range ns {
		if isCaipKey(key) {
			out[key] = actions{v.Methods, v.Events}
			continue
		}
		for _, c := range v.Chains {
			out[c] = actions{v.Methods, v.Events}
		}
	}
	return out
}

func parseApproved(ns Session) map[string]actions {
	out := map[string]actions{}
	for key, v := range ns {
		if isCaipKey(key) {
			out[key] = actions{v.Methods, v.Events}
			continue
		}
		for _, c := range AccountsChains(v.Accounts) {
			out[c] = actions{v.Methods, v.Events}
		}
	}
	return out
}

func uniqueNamespaceKeys(keys []string) []string {
	var out []string
	for _, k := range keys {
		k = namespaceKey(k)
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// subset is the JS hasOverlap(a, b): every element of a is in b.
func subset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}
