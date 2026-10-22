package walletconnect

import (
	"github.com/WalletConnect/walletconnect-go/internal/engine"
	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

type (
	// Metadata describes the app to its peer.
	Metadata = types.Metadata
	// Relay names the relay protocol of a session.
	Relay = types.Relay
	// Participant is one side of a session.
	Participant = types.Participant
	// Session is a settled session.
	Session = types.Session
	// Proposal is a session proposal.
	Proposal = types.Proposal
	// PendingRequest is a session request a wallet has not answered yet.
	PendingRequest = types.PendingRequest
	// ProposalNamespace is a namespace requested in a proposal (CAIP-25).
	ProposalNamespace = types.ProposalNamespace
	// SessionNamespace is a namespace approved for a session (CAIP-25).
	SessionNamespace = types.SessionNamespace
	// RPCError is a JSON-RPC error object sent by or to a peer.
	RPCError = jsonrpc.Error

	// ConnectParams configures a dApp's session proposal.
	ConnectParams = engine.ConnectParams
	// Connection is a pending dApp proposal; Wait for the wallet's answer.
	Connection = engine.Connection
	// ApproveParams configures a wallet's approval of a proposal.
	ApproveParams = engine.ApproveParams
	// Approval is an approved session; Acknowledged waits for the dApp.
	Approval = engine.Approval
	// Acknowledgement is a sent update or extend; Wait for the peer's ack.
	Acknowledgement = engine.Acknowledgement
	// RequestParams configures a dApp's session request.
	RequestParams = engine.RequestParams
)

// VerificationUnknown is the verification status of every proposal and
// request: the Verify API is not implemented.
const VerificationUnknown = types.VerificationUnknown
