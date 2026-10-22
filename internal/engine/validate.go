package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/namespaces"
	"github.com/WalletConnect/walletconnect-go/internal/sdkerr"
	"github.com/WalletConnect/walletconnect-go/internal/store"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

func invalid(err *jsonrpc.Error) error {
	return fmt.Errorf("%w: %s", types.ErrInvalidParams, err.Message)
}

func namespaceKeys[V any](m map[string]V) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		ns, _, _ := strings.Cut(k, ":")
		out[ns] = true
	}
	return out
}

// scopedSubset checks every scoped property key's namespace is in allowed.
func scopedSubset(scoped map[string]json.RawMessage, allowed map[string]bool) error {
	for k := range scoped {
		ns, _, _ := strings.Cut(k, ":")
		if !allowed[ns] {
			return fmt.Errorf("%w: scoped property %q is not in the namespaces", types.ErrInvalidParams, k)
		}
	}
	return nil
}

var hexKey = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

func validateProposal(p *proposeParams) *jsonrpc.Error {
	for _, r := range p.Relays {
		if r.Protocol == "" {
			return sdkerr.New(sdkerr.MissingOrInvalid, "connect() relays")
		}
	}
	if !hexKey.MatchString(p.Proposer.PublicKey) {
		return sdkerr.New(sdkerr.MissingOrInvalid, "proposer public key")
	}
	if len(p.RequiredNamespaces) > 0 {
		if err := namespaces.ValidateProposal(p.RequiredNamespaces, "connect()", "requiredNamespaces"); err != nil {
			return err
		}
	}
	if len(p.OptionalNamespaces) > 0 {
		if err := namespaces.ValidateProposal(p.OptionalNamespaces, "connect()", "optionalNamespaces"); err != nil {
			return err
		}
	}
	allowed := namespaceKeys(p.RequiredNamespaces)
	for k := range namespaceKeys(p.OptionalNamespaces) {
		allowed[k] = true
	}
	if scopedSubset(p.ScopedProperties, allowed) != nil {
		return sdkerr.New(sdkerr.MissingOrInvalid, "scopedProperties must be a subset of required/optional namespaces")
	}
	return nil
}

func validateSettle(p *settleParams, now int64) *jsonrpc.Error {
	if p.Relay.Protocol == "" {
		return sdkerr.New(sdkerr.MissingOrInvalid, "onSessionSettleRequest() relay protocol should be a string")
	}
	if p.Controller.PublicKey == "" {
		return sdkerr.New(sdkerr.MissingOrInvalid, "onSessionSettleRequest() controller public key should be a string")
	}
	if err := namespaces.ValidateSession(p.Namespaces, "onSessionSettleRequest()"); err != nil {
		return err
	}
	if p.Expiry < now {
		return sdkerr.New(sdkerr.Expired, "onSessionSettleRequest()")
	}
	return nil
}

// validateApproval checks a wallet's approved namespaces against the proposal.
func validateApproval(prop *store.ProposalRecord, ns namespaces.Session, scoped map[string]json.RawMessage) error {
	if err := namespaces.ValidateSession(ns, "approve()"); err != nil {
		return invalid(err)
	}
	if err := namespaces.Conforming(prop.RequiredNamespaces, ns, "approve()"); err != nil {
		return invalid(err)
	}
	return scopedSubset(scoped, namespaceKeys(ns))
}

// validateConnect checks a dApp's proposal parameters and returns the
// normalized optional namespaces.
func validateConnect(p ConnectParams) (namespaces.Proposal, error) {
	optional := namespaces.NormalizeProposal(p.OptionalNamespaces)
	if len(optional) > 0 {
		if err := namespaces.ValidateProposal(optional, "connect()", "optionalNamespaces"); err != nil {
			return nil, invalid(err)
		}
	}
	return optional, scopedSubset(p.ScopedProperties, namespaceKeys(optional))
}

// validateRequest checks a dApp's request against the session and returns
// its TTL in seconds.
func validateRequest(s *store.SessionRecord, p RequestParams) (int64, error) {
	if !namespaces.IsValidChainForSession(s.Namespaces, p.ChainID) {
		return 0, fmt.Errorf("%w: chainId %q is not in the session", types.ErrInvalidParams, p.ChainID)
	}
	if !namespaces.IsValidMethodForSession(s.Namespaces, p.ChainID, p.Method) {
		return 0, fmt.Errorf("%w: method %q is not allowed on %s", types.ErrInvalidParams, p.Method, p.ChainID)
	}
	if p.Expiry == 0 {
		return requestTTLDefault, nil
	}
	ttl := int64(p.Expiry / time.Second)
	if ttl < requestTTLMin || ttl > requestTTLMax {
		return 0, fmt.Errorf("%w: expiry must be between 5m and 168h", types.ErrInvalidParams)
	}
	return ttl, nil
}
