package namespaces

import (
	"encoding/json"
	"testing"

	"github.com/WalletConnect/walletconnect-go/internal/testvectors"
)

func TestConformingVectors(t *testing.T) {
	for _, c := range testvectors.Load(t).Namespaces.Conforming {
		var req Proposal
		var appr Session
		if err := json.Unmarshal(c.Required, &req); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(c.Approved, &appr); err != nil {
			t.Fatal(err)
		}
		got := 0
		if err := Conforming(req, appr, "approve()"); err != nil {
			got = err.Code
		}
		if got != c.ErrorCode {
			t.Errorf("%s: code = %d, want %d", c.Name, got, c.ErrorCode)
		}
	}
}

func TestSessionVectors(t *testing.T) {
	for _, c := range testvectors.Load(t).Namespaces.Session {
		var ns Session
		if err := json.Unmarshal(c.Namespaces, &ns); err != nil {
			t.Fatal(err)
		}
		got := 0
		if err := ValidateSession(ns, "approve()"); err != nil {
			got = err.Code
		}
		if got != c.ErrorCode {
			t.Errorf("%s: code = %d, want %d", c.Name, got, c.ErrorCode)
		}
	}
}

func TestProposalVectors(t *testing.T) {
	for _, c := range testvectors.Load(t).Namespaces.Proposal {
		var ns Proposal
		if err := json.Unmarshal(c.Namespaces, &ns); err != nil {
			t.Fatal(err)
		}
		got := 0
		if err := ValidateProposal(ns, "connect()", "optionalNamespaces"); err != nil {
			got = err.Code
		}
		if got != c.ErrorCode {
			t.Errorf("%s: code = %d, want %d", c.Name, got, c.ErrorCode)
		}
	}
}

func TestSessionHelpers(t *testing.T) {
	ns := Session{"eip155": {Accounts: []string{"eip155:1:0xa", "eip155:10:0xa"}, Methods: []string{"personal_sign"}, Events: []string{"chainChanged"}}}
	if !IsValidChainForSession(ns, "eip155:10") || IsValidChainForSession(ns, "eip155:5") {
		t.Fatal("chain check")
	}
	if !IsValidMethodForSession(ns, "eip155:1", "personal_sign") || IsValidMethodForSession(ns, "eip155:1", "eth_sign") {
		t.Fatal("method check")
	}
	if !IsValidEventForSession(ns, "eip155:1", "chainChanged") || IsValidEventForSession(ns, "eip155:1", "x") {
		t.Fatal("event check")
	}
}
