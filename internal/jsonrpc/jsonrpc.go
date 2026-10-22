// Package jsonrpc holds the JSON-RPC 2.0 envelopes and ID schemes shared by
// the relay transport and the peer-to-peer protocol.

package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const Version = "2.0"

// Error is the JSON-RPC error object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

type Request struct {
	ID      int64  `json:"id"`
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type Result struct {
	ID      int64  `json:"id"`
	JSONRPC string `json:"jsonrpc"`
	Result  any    `json:"result"`
}

type ErrorResponse struct {
	ID      int64  `json:"id"`
	JSONRPC string `json:"jsonrpc"`
	Error   *Error `json:"error"`
}

// Message is a decoded peer payload that may be a request or a response.
type Message struct {
	ID     int64
	Method string
	Params json.RawMessage
	Result json.RawMessage
	Error  *Error
}

func (m *Message) IsRequest() bool { return m.Method != "" }

func (m *Message) IsResponse() bool { return m.Method == "" && (m.Result != nil || m.Error != nil) }

var ErrNotJSONRPC = errors.New("jsonrpc: payload is neither a request nor a response")

func Parse(b []byte) (*Message, error) {
	var raw struct {
		ID     json.Number     `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	id, err := raw.ID.Int64()
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: invalid id %q: %w", raw.ID, err)
	}
	m := &Message{ID: id, Method: raw.Method, Params: raw.Params, Result: raw.Result, Error: raw.Error}
	if !m.IsRequest() && !m.IsResponse() {
		return nil, ErrNotJSONRPC
	}
	return m, nil
}
