// Package walletconnect is a Go SDK for the WalletConnect Sign v2 protocol.
// One Client can act as a dApp, proposing sessions and sending requests, and
// as a wallet, approving sessions and answering requests. It interoperates
// with the JS SDK over the production relay.
//
// # dApp
//
// Connect creates a pairing and sends a session proposal; show
// Connection.URI to the wallet as a QR code or deep link, then wait for the
// session with Connection.Wait. Request sends a JSON-RPC request in the
// session and returns the wallet's result.
//
// # Wallet
//
// Pair takes the URI a dApp shows. Proposals and requests then arrive on
// Events as EventSessionProposal and EventSessionRequest; answer them with
// Approve or Reject, and Respond or RespondError.
//
// # State
//
// Sessions, pairings, proposals and pending requests live in
// Options.Storage, so a client restarted on the same storage resumes them.
// The default storage is in memory; storage.OpenBolt keeps state in a file.
//
// # Errors
//
// Client methods return the Err* values, compared with errors.Is, for the
// SDK's own failures. A peer's error answer, such as a user rejecting a
// request, is an *RPCError; SDKError builds the protocol's standard ones.
//
// # Observability
//
// Options.Trace logs every action from start to finish, Options.TraceFile
// keeps the same spans in a file the tracefile package and the wctrace
// command read, and Options.TracerProvider and Options.MeterProvider send
// them to OpenTelemetry. Like the JS SDK, the client reports each API call
// to WalletConnect's Pulse API without params, payloads, keys or
// addresses; Options.NoTelemetry turns that off.
package walletconnect
