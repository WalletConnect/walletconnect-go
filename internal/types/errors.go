package types

import "errors"

var (
	ErrNotFound       = errors.New("walletconnect: not found")
	ErrExpired        = errors.New("walletconnect: expired")
	ErrInvalidAuth    = errors.New("walletconnect: relay rejected the client auth")
	ErrTimeout        = errors.New("walletconnect: relay request timed out")
	ErrSessionDeleted = errors.New("walletconnect: session deleted")
	ErrPairingExists  = errors.New("walletconnect: pairing already exists")
	ErrClosed         = errors.New("walletconnect: client closed")
	ErrUnauthorized   = errors.New("walletconnect: only the session controller can do this")
	ErrInvalidParams  = errors.New("walletconnect: invalid params")
)
