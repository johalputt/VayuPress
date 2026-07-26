// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"net/http"
)

// JSON-RPC 2.0 error codes used by this server.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// rpcRequest is an incoming JSON-RPC 2.0 request. A nil ID marks a notification
// (no response is written).
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	// ID is REQUIRED on every JSON-RPC 2.0 response and is Null when it can't be
	// determined (parse/invalid-request errors), so it is NOT omitempty: a nil
	// RawMessage marshals to `null`. Success responses always carry the request id.
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

// writeToolError returns a successful JSON-RPC response whose *tool result* is
// flagged isError — the MCP convention for a tool that ran but failed, so the
// model sees the reason instead of a transport error.
func writeToolError(w http.ResponseWriter, id json.RawMessage, msg string) {
	writeResult(w, id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	})
}

func writeHTTPError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
