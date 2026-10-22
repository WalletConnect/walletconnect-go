package walletconnect

import (
	"github.com/WalletConnect/walletconnect-go/internal/sdkerr"
	"github.com/WalletConnect/walletconnect-go/internal/types"
)

var (
	ErrNotFound       = types.ErrNotFound
	ErrExpired        = types.ErrExpired
	ErrInvalidAuth    = types.ErrInvalidAuth
	ErrTimeout        = types.ErrTimeout
	ErrSessionDeleted = types.ErrSessionDeleted
	ErrPairingExists  = types.ErrPairingExists
	ErrClosed         = types.ErrClosed
	ErrUnauthorized   = types.ErrUnauthorized
	ErrInvalidParams  = types.ErrInvalidParams
)

// SDKErrorKey names an entry of the protocol error table.
type SDKErrorKey = sdkerr.Key

const (
	ErrKeyUserRejected            = sdkerr.UserRejected
	ErrKeyUserRejectedChains      = sdkerr.UserRejectedChains
	ErrKeyUserRejectedMethods     = sdkerr.UserRejectedMethods
	ErrKeyUserRejectedEvents      = sdkerr.UserRejectedEvents
	ErrKeyUnsupportedChains       = sdkerr.UnsupportedChains
	ErrKeyUnsupportedMethods      = sdkerr.UnsupportedMethods
	ErrKeyUnsupportedEvents       = sdkerr.UnsupportedEvents
	ErrKeyUnsupportedAccounts     = sdkerr.UnsupportedAccounts
	ErrKeyUnsupportedNamespaceKey = sdkerr.UnsupportedNamespaceKey
	ErrKeyUserDisconnected        = sdkerr.UserDisconnected
	ErrKeyInvalidMethod           = sdkerr.InvalidMethod
	ErrKeyInvalidEvent            = sdkerr.InvalidEvent
	ErrKeyUnauthorizedMethod      = sdkerr.UnauthorizedMethod
	ErrKeyUnauthorizedEvent       = sdkerr.UnauthorizedEvent
	ErrKeySessionSettlementFailed = sdkerr.SessionSettlementFailed
	ErrKeyWCMethodUnsupported     = sdkerr.WCMethodUnsupported
)

// SDKError returns the protocol error for key, with context appended to its
// message, e.g. SDKError(ErrKeyUserRejected, "") for a rejected request.
func SDKError(key SDKErrorKey, context string) *RPCError { return sdkerr.New(key, context) }
