// Package types holds the protocol and API types shared by the engine, the
// store and the public package, which re-exports them as aliases.

package types

import (
	"encoding/json"

	"github.com/WalletConnect/walletconnect-go/internal/namespaces"
)

type Metadata struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	URL         string          `json:"url"`
	Icons       []string        `json:"icons"`
	VerifyURL   string          `json:"verifyUrl,omitempty"`
	Redirect    json.RawMessage `json:"redirect,omitempty"`
}

// Normalized returns a copy whose Icons marshal as [] rather than null.
func (m Metadata) Normalized() Metadata {
	if m.Icons == nil {
		m.Icons = []string{}
	}
	return m
}

type Relay struct {
	Protocol string `json:"protocol"`
	Data     string `json:"data,omitempty"`
}

type Participant struct {
	PublicKey string   `json:"publicKey"`
	Metadata  Metadata `json:"metadata"`
}

type ProposalNamespace = namespaces.ProposalNamespace

type SessionNamespace = namespaces.SessionNamespace

// Session is a settled session. The symmetric key is kept out of this type.
type Session struct {
	Topic              string                       `json:"topic"`
	PairingTopic       string                       `json:"pairingTopic"`
	Relay              Relay                        `json:"relay"`
	Expiry             int64                        `json:"expiry"`
	Acknowledged       bool                         `json:"acknowledged"`
	Controller         string                       `json:"controller"`
	Namespaces         map[string]SessionNamespace  `json:"namespaces"`
	RequiredNamespaces map[string]ProposalNamespace `json:"requiredNamespaces"`
	OptionalNamespaces map[string]ProposalNamespace `json:"optionalNamespaces"`
	SessionProperties  map[string]string            `json:"sessionProperties,omitempty"`
	ScopedProperties   map[string]json.RawMessage   `json:"scopedProperties,omitempty"`
	SessionConfig      json.RawMessage              `json:"sessionConfig,omitempty"`
	Self               Participant                  `json:"self"`
	Peer               Participant                  `json:"peer"`
}

// Proposal is a session proposal, as received by a wallet or sent by a dApp.
type Proposal struct {
	ID                 int64                        `json:"id"`
	PairingTopic       string                       `json:"pairingTopic"`
	ExpiryTimestamp    int64                        `json:"expiryTimestamp"`
	Relays             []Relay                      `json:"relays"`
	Proposer           Participant                  `json:"proposer"`
	RequiredNamespaces map[string]ProposalNamespace `json:"requiredNamespaces"`
	OptionalNamespaces map[string]ProposalNamespace `json:"optionalNamespaces"`
	SessionProperties  map[string]string            `json:"sessionProperties,omitempty"`
	ScopedProperties   map[string]json.RawMessage   `json:"scopedProperties,omitempty"`
}

// PendingRequest is a session request a wallet has received and not answered.
type PendingRequest struct {
	ID              int64           `json:"id"`
	Topic           string          `json:"topic"`
	ChainID         string          `json:"chainId"`
	Method          string          `json:"method"`
	Params          json.RawMessage `json:"params"`
	ExpiryTimestamp int64           `json:"expiryTimestamp"`
}

// Verification is always "UNKNOWN": the Verify API is not implemented.
const VerificationUnknown = "UNKNOWN"
