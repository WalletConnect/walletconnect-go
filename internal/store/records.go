package store

import (
	"encoding/json"

	"github.com/WalletConnect/walletconnect-go/internal/types"
)

// Pairing roles: the dApp creates a pairing, the wallet joins it.
const (
	RoleDApp   = "dapp"
	RoleWallet = "wallet"
)

// History directions: requests this client sent or received.
const (
	DirectionIn  = "in"
	DirectionOut = "out"
)

// PendingSessionTTL bounds how long a dApp waits for the settle request
// after the wallet answered its proposal, in seconds.
const PendingSessionTTL = 300

// Pairing is a pairing topic and its key. ProposalID is the proposal a dApp
// sent on it. A wallet retires a pairing once it has answered the proposal:
// the record stays until it expires so the same URI cannot be paired again,
// but it is no longer subscribed to or decrypted.
type Pairing struct {
	Topic      string `json:"topic"`
	SymKey     string `json:"symKey"`
	Expiry     int64  `json:"expiry"`
	Role       string `json:"role"`
	ProposalID int64  `json:"proposalId,omitempty"`
	Retired    bool   `json:"retired,omitempty"`
}

// ProposalRecord is a proposal. SelfPrivateKey is set only on the dApp side,
// which needs it to derive the session key from the wallet's answer.
type ProposalRecord struct {
	types.Proposal
	SelfPrivateKey string `json:"selfPrivateKey,omitempty"`
}

// PendingSession is the dApp's session key between the wallet's proposal
// response and its settle request.
type PendingSession struct {
	SessionTopic  string `json:"sessionTopic"`
	SymKey        string `json:"symKey"`
	ProposalID    int64  `json:"proposalId"`
	PairingTopic  string `json:"pairingTopic"`
	SelfPublicKey string `json:"selfPublicKey"`
	CreatedAt     int64  `json:"createdAt"`
}

// SessionRecord is a settled session and its key.
type SessionRecord struct {
	types.Session
	SymKey string `json:"symKey"`
}

// History records a JSON-RPC request this client sent or received and its
// response. It routes responses and drops redelivered requests.
type History struct {
	ID        int64           `json:"id"`
	Topic     string          `json:"topic"`
	Method    string          `json:"method"`
	Request   json.RawMessage `json:"request,omitempty"`
	Response  json.RawMessage `json:"response,omitempty"`
	Direction string          `json:"direction"`
	CreatedAt int64           `json:"createdAt"`
}
