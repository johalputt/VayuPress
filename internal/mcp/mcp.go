// SPDX-License-Identifier: Apache-2.0

// Package mcp implements a minimal, host-agnostic Model Context Protocol (MCP)
// server over Streamable HTTP + JSON-RPC 2.0. It knows nothing about VayuPress:
// the host registers Tools (name, description, input schema, and a handler
// closure) and mounts ServeHTTP on any route. This keeps the protocol small,
// dependency-free, and unit-testable without a live server.
//
// Supported methods: initialize, notifications/initialized (notification),
// tools/list, tools/call, ping. Unknown methods return JSON-RPC -32601.
package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"sync"
)

// protocolVersion is the MCP revision this server implements. If a client
// requests a different version we echo ours; MCP clients negotiate down.
const protocolVersion = "2025-06-18"

// maxRequestBytes bounds a single JSON-RPC request body (defence against a
// runaway client). Tool arguments for VayuPress (article HTML up to ~5 MB) fit
// comfortably under this.
const maxRequestBytes = 8 << 20

// Tool is one callable capability exposed to the MCP client.
type Tool struct {
	Name        string
	Description string
	// InputSchema is a JSON Schema object describing the tool's arguments. It is
	// sent verbatim in tools/list. Use NewObjectSchema to build one.
	InputSchema map[string]any
	// Visible reports whether this tool should be listed/callable for the current
	// request (e.g. the caller's key grants its capability). nil ⇒ always visible.
	Visible func(ctx context.Context) bool
	// Handler runs the tool. args is the raw JSON arguments object. It returns the
	// text result (shown to the model) or an error. A returned error becomes an
	// MCP tool error (isError=true), not a transport error.
	Handler func(ctx context.Context, args json.RawMessage) (string, error)
}

// Server holds the registered tools and server identity.
type Server struct {
	name    string
	version string

	mu    sync.RWMutex
	tools []Tool
	index map[string]int
}

// NewServer creates an MCP server with the given advertised name/version.
func NewServer(name, version string) *Server {
	return &Server{name: name, version: version, index: map[string]int{}}
}

// Register adds (or replaces) a tool. Not safe to call concurrently with
// ServeHTTP; register all tools at startup.
func (s *Server) Register(t Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, ok := s.index[t.Name]; ok {
		s.tools[i] = t
		return
	}
	s.index[t.Name] = len(s.tools)
	s.tools = append(s.tools, t)
}

// Tools returns every registered tool, ignoring visibility.
//
// Exported for tests that assert properties of the SURFACE rather than of one
// caller's view of it — notably that the VayuShield tools are all read-only. A
// visibility-filtered list would let a mutation hide behind a permission the test
// happens not to hold, which is the opposite of what such a test is for.
func (s *Server) Tools() []Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Tool, len(s.tools))
	copy(out, s.tools)
	return out
}

// visibleTools returns the tools visible for this request context, sorted by
// name for a stable listing.
func (s *Server) visibleTools(ctx context.Context) []Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Tool, 0, len(s.tools))
	for _, t := range s.tools {
		if t.Visible == nil || t.Visible(ctx) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Server) lookup(name string) (Tool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if i, ok := s.index[name]; ok {
		return s.tools[i], true
	}
	return Tool{}, false
}

// ServeHTTP handles one JSON-RPC request (Streamable HTTP, non-streaming). The
// host is expected to have already authenticated the request and stamped any
// identity into ctx before this runs.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeHTTPError(w, http.StatusMethodNotAllowed, "MCP endpoint accepts POST (JSON-RPC 2.0)")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes))
	if err != nil {
		writeRPCError(w, nil, codeParseError, "could not read request body")
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, nil, codeParseError, "invalid JSON")
		return
	}
	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, codeInvalidRequest, "jsonrpc must be \"2.0\"")
		return
	}

	// Notifications (no id) get no response body; only "initialized" is expected.
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		s.handleInitialize(w, req)
	case "ping":
		writeResult(w, req.ID, map[string]any{})
	case "tools/list":
		s.handleToolsList(w, r.Context(), req)
	case "tools/call":
		s.handleToolsCall(w, r.Context(), req)
	default:
		writeRPCError(w, req.ID, codeMethodNotFound, "unknown method: "+req.Method)
	}
}

func (s *Server) handleInitialize(w http.ResponseWriter, req rpcRequest) {
	writeResult(w, req.ID, map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{"name": s.name, "version": s.version},
	})
}

func (s *Server) handleToolsList(w http.ResponseWriter, ctx context.Context, req rpcRequest) {
	tools := s.visibleTools(ctx)
	list := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		schema := t.InputSchema
		if schema == nil {
			schema = NewObjectSchema()
		}
		list = append(list, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schema,
		})
	}
	writeResult(w, req.ID, map[string]any{"tools": list})
}

func (s *Server) handleToolsCall(w http.ResponseWriter, ctx context.Context, req rpcRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeRPCError(w, req.ID, codeInvalidParams, "invalid params")
			return
		}
	}
	if params.Name == "" {
		writeRPCError(w, req.ID, codeInvalidParams, "missing tool name")
		return
	}
	t, ok := s.lookup(params.Name)
	// A tool the caller can't see is reported as unknown — never disclose the
	// existence of a capability the key does not grant.
	if !ok || (t.Visible != nil && !t.Visible(ctx)) {
		writeToolError(w, req.ID, "unknown or unavailable tool: "+params.Name)
		return
	}
	out, herr := t.Handler(ctx, params.Arguments)
	if herr != nil {
		writeToolError(w, req.ID, herr.Error())
		return
	}
	writeResult(w, req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": out}},
		"isError": false,
	})
}

// NewObjectSchema returns an empty JSON-Schema object ({"type":"object",
// "properties":{}}). Callers add properties and a "required" list.
func NewObjectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
