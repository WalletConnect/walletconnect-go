// Package sdkerr mirrors the SDK and internal error tables of the JS SDK.
package sdkerr

import "github.com/WalletConnect/walletconnect-go/internal/jsonrpc"

type Key string

const (
	InvalidMethod               Key = "INVALID_METHOD"
	InvalidEvent                Key = "INVALID_EVENT"
	InvalidUpdateRequest        Key = "INVALID_UPDATE_REQUEST"
	InvalidExtendRequest        Key = "INVALID_EXTEND_REQUEST"
	InvalidSessionSettleRequest Key = "INVALID_SESSION_SETTLE_REQUEST"
	UnauthorizedMethod          Key = "UNAUTHORIZED_METHOD"
	UnauthorizedEvent           Key = "UNAUTHORIZED_EVENT"
	UnauthorizedUpdateRequest   Key = "UNAUTHORIZED_UPDATE_REQUEST"
	UnauthorizedExtendRequest   Key = "UNAUTHORIZED_EXTEND_REQUEST"
	UserRejected                Key = "USER_REJECTED"
	UserRejectedChains          Key = "USER_REJECTED_CHAINS"
	UserRejectedMethods         Key = "USER_REJECTED_METHODS"
	UserRejectedEvents          Key = "USER_REJECTED_EVENTS"
	UnsupportedChains           Key = "UNSUPPORTED_CHAINS"
	UnsupportedMethods          Key = "UNSUPPORTED_METHODS"
	UnsupportedEvents           Key = "UNSUPPORTED_EVENTS"
	UnsupportedAccounts         Key = "UNSUPPORTED_ACCOUNTS"
	UnsupportedNamespaceKey     Key = "UNSUPPORTED_NAMESPACE_KEY"
	UserDisconnected            Key = "USER_DISCONNECTED"
	SessionSettlementFailed     Key = "SESSION_SETTLEMENT_FAILED"
	WCMethodUnsupported         Key = "WC_METHOD_UNSUPPORTED"

	NotInitialized          Key = "NOT_INITIALIZED"
	NoMatchingKey           Key = "NO_MATCHING_KEY"
	MissingOrInvalid        Key = "MISSING_OR_INVALID"
	Expired                 Key = "EXPIRED"
	UnknownType             Key = "UNKNOWN_TYPE"
	MismatchedTopic         Key = "MISMATCHED_TOPIC"
	NonConformingNamespaces Key = "NON_CONFORMING_NAMESPACES"
)

type entry struct {
	code    int
	message string
}

var table = map[Key]entry{
	InvalidMethod:               {1001, "Invalid method."},
	InvalidEvent:                {1002, "Invalid event."},
	InvalidUpdateRequest:        {1003, "Invalid update request."},
	InvalidExtendRequest:        {1004, "Invalid extend request."},
	InvalidSessionSettleRequest: {1005, "Invalid session settle request."},
	UnauthorizedMethod:          {3001, "Unauthorized method."},
	UnauthorizedEvent:           {3002, "Unauthorized event."},
	UnauthorizedUpdateRequest:   {3003, "Unauthorized update request."},
	UnauthorizedExtendRequest:   {3004, "Unauthorized extend request."},
	UserRejected:                {5000, "User rejected."},
	UserRejectedChains:          {5001, "User rejected chains."},
	UserRejectedMethods:         {5002, "User rejected methods."},
	UserRejectedEvents:          {5003, "User rejected events."},
	UnsupportedChains:           {5100, "Unsupported chains."},
	UnsupportedMethods:          {5101, "Unsupported methods."},
	UnsupportedEvents:           {5102, "Unsupported events."},
	UnsupportedAccounts:         {5103, "Unsupported accounts."},
	UnsupportedNamespaceKey:     {5104, "Unsupported namespace key."},
	UserDisconnected:            {6000, "User disconnected."},
	SessionSettlementFailed:     {7000, "Session settlement failed."},
	WCMethodUnsupported:         {10001, "Unsupported wc_ method."},

	NotInitialized:          {1, "Not initialized."},
	NoMatchingKey:           {2, "No matching key."},
	MissingOrInvalid:        {5, "Missing or invalid."},
	Expired:                 {6, "Expired."},
	UnknownType:             {7, "Unknown type."},
	MismatchedTopic:         {8, "Mismatched topic."},
	NonConformingNamespaces: {9, "Non conforming namespaces."},
}

// New returns the error object, appending context to the message like JS.
func New(k Key, context string) *jsonrpc.Error {
	e, ok := table[k]
	if !ok {
		e = entry{5, "Missing or invalid."}
	}
	msg := e.message
	if context != "" {
		msg += " " + context
	}
	return &jsonrpc.Error{Code: e.code, Message: msg}
}
