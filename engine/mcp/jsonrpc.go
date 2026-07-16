// Package mcp is a minimal client for the Model Context Protocol (MCP). It
// speaks JSON-RPC 2.0 over a pluggable transport (stdio subprocess or Streamable
// HTTP) and exposes the tools a server offers as runcode tools.
//
// The protocol layer is deliberately transport-agnostic: a Client talks to a
// conn, which frames JSON-RPC over a messageStream. Transports implement
// messageStream, so the protocol logic is unit-tested over an in-memory pipe
// without spawning a process or opening a socket.
package mcp

import (
	"encoding/json"
	"fmt"
)

// jsonRPCVersion is the only JSON-RPC version MCP uses.
const jsonRPCVersion = "2.0"

// rpcMessage is a JSON-RPC 2.0 envelope covering requests, responses, and
// notifications. A request/response carries an ID; a notification omits it. A
// response carries Result or Error.
type rpcMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return "<nil rpc error>"
	}
	if len(e.Data) > 0 {
		return fmt.Sprintf("mcp rpc error %d: %s (%s)", e.Code, e.Message, string(e.Data))
	}
	return fmt.Sprintf("mcp rpc error %d: %s", e.Code, e.Message)
}

// encodeRequest builds a JSON-RPC request frame with a numeric id.
func encodeRequest(id int64, method string, params any) ([]byte, error) {
	rawID, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	idMsg := json.RawMessage(rawID)
	return marshalMessage(method, &idMsg, params)
}

// encodeNotification builds a JSON-RPC notification frame (no id).
func encodeNotification(method string, params any) ([]byte, error) {
	return marshalMessage(method, nil, params)
}

func marshalMessage(method string, id *json.RawMessage, params any) ([]byte, error) {
	msg := rpcMessage{JSONRPC: jsonRPCVersion, Method: method, ID: id}
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("mcp: encode params: %w", err)
		}
		msg.Params = encoded
	}
	return json.Marshal(msg)
}

// messageID extracts a numeric request id from a raw JSON id. Non-numeric ids
// (which runcode never sends) report ok=false so they are not mismatched.
func messageID(raw *json.RawMessage) (int64, bool) {
	if raw == nil {
		return 0, false
	}
	var id int64
	if err := json.Unmarshal(*raw, &id); err != nil {
		return 0, false
	}
	return id, true
}
