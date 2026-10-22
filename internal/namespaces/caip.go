package namespaces

import "strings"

func IsValidChainID(s string) bool {
	return s != "" && strings.Contains(s, ":") && len(strings.Split(s, ":")) == 2
}

func IsValidAccountID(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 3 || parts[2] == "" {
		return false
	}
	return IsValidChainID(parts[0] + ":" + parts[1])
}

// AccountsChains maps namespace:chainId:address to namespace:chainId.
func AccountsChains(accounts []string) []string {
	out := make([]string, 0, len(accounts))
	for _, a := range accounts {
		parts := strings.SplitN(a, ":", 3)
		if len(parts) >= 2 {
			out = append(out, parts[0]+":"+parts[1])
		}
	}
	return out
}
