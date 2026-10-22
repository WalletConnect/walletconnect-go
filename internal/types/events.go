package types

import (
	"encoding/json"

	"github.com/WalletConnect/walletconnect-go/internal/jsonrpc"
)

// Event is delivered on Client.Events(). Switch on the concrete type.
type Event interface{ isEvent() }

type EventSessionProposal struct {
	Proposal     Proposal
	Verification string
}

type EventSessionConnect struct{ Session Session }

type EventSessionRequest struct {
	Topic           string
	ID              int64
	ChainID         string
	Method          string
	Params          json.RawMessage
	ExpiryTimestamp int64
	Verification    string
}

type EventSessionEvent struct {
	Topic   string
	ID      int64
	ChainID string
	Name    string
	Data    json.RawMessage
}

type EventSessionUpdate struct {
	Topic      string
	ID         int64
	Namespaces map[string]SessionNamespace
}

type EventSessionExtend struct {
	Topic  string
	ID     int64
	Expiry int64
}

type EventSessionPing struct {
	Topic string
	ID    int64
}

type EventSessionDelete struct {
	Topic  string
	ID     int64
	Reason *jsonrpc.Error
}

type EventSessionExpire struct{ Topic string }

type EventProposalExpire struct{ ProposalID int64 }

type EventRequestExpire struct {
	Topic string
	ID    int64
}

type EventRelayState struct {
	Connected bool
	Err       error
}

func (EventSessionProposal) isEvent() {}
func (EventSessionConnect) isEvent()  {}
func (EventSessionRequest) isEvent()  {}
func (EventSessionEvent) isEvent()    {}
func (EventSessionUpdate) isEvent()   {}
func (EventSessionExtend) isEvent()   {}
func (EventSessionPing) isEvent()     {}
func (EventSessionDelete) isEvent()   {}
func (EventSessionExpire) isEvent()   {}
func (EventProposalExpire) isEvent()  {}
func (EventRequestExpire) isEvent()   {}
func (EventRelayState) isEvent()      {}
