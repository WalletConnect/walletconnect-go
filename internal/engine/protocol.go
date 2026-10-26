package engine

import (
	"encoding/json"

	"github.com/WalletConnect/walletconnect-go/internal/types"
)

const (
	methodPropose      = "wc_sessionPropose"
	methodSettle       = "wc_sessionSettle"
	methodUpdate       = "wc_sessionUpdate"
	methodExtend       = "wc_sessionExtend"
	methodRequest      = "wc_sessionRequest"
	methodEvent        = "wc_sessionEvent"
	methodDelete       = "wc_sessionDelete"
	methodPing         = "wc_sessionPing"
	methodAuthenticate = "wc_sessionAuthenticate"

	fiveMinutes = 300
	oneDay      = 86400
	sevenDays   = 7 * oneDay
	thirtyDays  = 30 * oneDay

	requestTTLDefault = 3 * fiveMinutes
	requestTTLMin     = fiveMinutes
	requestTTLMax     = sevenDays

	relayProtocol = "irn"
)

// publishOpts holds the relay tag, TTL and prompt flag of one message.
type publishOpts struct {
	tag    int
	ttl    int64
	prompt bool
	tnv    *tnv // relay-side data about the message; see tnv.go
}

// with returns a copy of o carrying t.
func (o publishOpts) with(t *tnv) publishOpts {
	o.tnv = t
	return o
}

// Tags and TTLs from packages/sign-client/src/constants/engine.ts.
var (
	optsProposeReject     = publishOpts{tag: 1120, ttl: fiveMinutes}
	optsProposeAutoReject = publishOpts{tag: 1121, ttl: fiveMinutes}
	optsSettleRes         = publishOpts{tag: 1103, ttl: fiveMinutes}
	optsUpdateReq         = publishOpts{tag: 1104, ttl: oneDay}
	optsUpdateRes         = publishOpts{tag: 1105, ttl: oneDay}
	optsExtendReq         = publishOpts{tag: 1106, ttl: oneDay}
	optsExtendRes         = publishOpts{tag: 1107, ttl: oneDay}
	optsRequestReq        = publishOpts{tag: 1108, ttl: requestTTLDefault, prompt: true}
	optsRequestRes        = publishOpts{tag: 1109, ttl: requestTTLDefault}
	optsEventReq          = publishOpts{tag: 1110, ttl: fiveMinutes, prompt: true}
	optsEventRes          = publishOpts{tag: 1111, ttl: fiveMinutes}
	optsDeleteReq         = publishOpts{tag: 1112, ttl: oneDay}
	optsPingReq           = publishOpts{tag: 1114, ttl: oneDay}
	optsPingRes           = publishOpts{tag: 1115, ttl: oneDay}
)

type proposeParams struct {
	RequiredNamespaces map[string]types.ProposalNamespace `json:"requiredNamespaces"`
	OptionalNamespaces map[string]types.ProposalNamespace `json:"optionalNamespaces"`
	Relays             []types.Relay                      `json:"relays"`
	Proposer           types.Participant                  `json:"proposer"`
	ExpiryTimestamp    int64                              `json:"expiryTimestamp,omitempty"`
	PairingTopic       string                             `json:"pairingTopic,omitempty"`
	ID                 int64                              `json:"id,omitempty"`
	SessionProperties  map[string]string                  `json:"sessionProperties,omitempty"`
	ScopedProperties   map[string]json.RawMessage         `json:"scopedProperties,omitempty"`
}

type proposeResult struct {
	Relay              types.Relay `json:"relay"`
	ResponderPublicKey string      `json:"responderPublicKey"`
}

type settleParams struct {
	Relay             types.Relay                       `json:"relay"`
	Namespaces        map[string]types.SessionNamespace `json:"namespaces"`
	Controller        types.Participant                 `json:"controller"`
	Expiry            int64                             `json:"expiry"`
	SessionProperties map[string]string                 `json:"sessionProperties,omitempty"`
	ScopedProperties  map[string]json.RawMessage        `json:"scopedProperties,omitempty"`
	SessionConfig     json.RawMessage                   `json:"sessionConfig,omitempty"`
}

type updateParams struct {
	Namespaces map[string]types.SessionNamespace `json:"namespaces"`
}

type sessionRequest struct {
	Method          string          `json:"method"`
	Params          json.RawMessage `json:"params"`
	ExpiryTimestamp int64           `json:"expiryTimestamp,omitempty"`
}

type requestParams struct {
	Request sessionRequest `json:"request"`
	ChainID string         `json:"chainId"`
}

type sessionEvent struct {
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

type eventParams struct {
	Event   sessionEvent `json:"event"`
	ChainID string       `json:"chainId"`
}

type empty struct{}
