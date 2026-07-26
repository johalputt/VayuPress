// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func call(t *testing.T, s *Server, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code == http.StatusAccepted {
		return nil // notification
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response not JSON: %q (%v)", rec.Body.String(), err)
	}
	return out
}

func newTestServer() *Server {
	s := NewServer("VayuMCP", "test")
	s.Register(Tool{
		Name:        "echo",
		Description: "echoes its message",
		InputSchema: NewObjectSchema(),
		Handler: func(_ context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(args, &in)
			return "you said: " + in.Message, nil
		},
	})
	s.Register(Tool{
		Name:        "boom",
		Description: "always fails",
		Handler:     func(context.Context, json.RawMessage) (string, error) { return "", errTest },
	})
	// A tool hidden unless ctx carries allow=true.
	s.Register(Tool{
		Name:        "secret",
		Description: "gated",
		Visible:     func(ctx context.Context) bool { return ctx.Value(ctxAllow{}) == true },
		Handler:     func(context.Context, json.RawMessage) (string, error) { return "ok", nil },
	})
	return s
}

type ctxAllow struct{}

var errTest = &testErr{"kaboom"}

type testErr struct{ s string }

func (e *testErr) Error() string { return e.s }

func TestInitialize(t *testing.T) {
	s := newTestServer()
	out := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	res, _ := out["result"].(map[string]any)
	if res["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v, want %s", res["protocolVersion"], protocolVersion)
	}
	info, _ := res["serverInfo"].(map[string]any)
	if info["name"] != "VayuMCP" {
		t.Errorf("serverInfo.name = %v", info["name"])
	}
}

func TestToolsListHidesGatedTools(t *testing.T) {
	s := newTestServer()
	// Default context: "secret" hidden.
	out := call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	res := out["result"].(map[string]any)
	names := toolNames(res)
	if names["secret"] {
		t.Error("gated tool 'secret' should be hidden without allow ctx")
	}
	if !names["echo"] || !names["boom"] {
		t.Errorf("expected echo+boom listed, got %v", names)
	}
	// With allow ctx the gated tool appears.
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	req = req.WithContext(context.WithValue(req.Context(), ctxAllow{}, true))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var out2 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out2)
	if !toolNames(out2["result"].(map[string]any))["secret"] {
		t.Error("gated tool 'secret' should appear with allow ctx")
	}
}

func TestToolsCallSuccess(t *testing.T) {
	s := newTestServer()
	out := call(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hi"}}}`)
	res := out["result"].(map[string]any)
	if res["isError"] != false {
		t.Errorf("isError = %v, want false", res["isError"])
	}
	text := firstText(res)
	if text != "you said: hi" {
		t.Errorf("text = %q", text)
	}
}

func TestToolsCallHandlerErrorIsToolError(t *testing.T) {
	s := newTestServer()
	out := call(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"boom","arguments":{}}}`)
	res := out["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("handler error must yield isError=true, got %v", res)
	}
	if firstText(res) != "kaboom" {
		t.Errorf("error text = %q", firstText(res))
	}
	if _, hasErr := out["error"]; hasErr {
		t.Error("handler error must NOT be a JSON-RPC transport error")
	}
}

func TestToolsCallHiddenToolLooksUnknown(t *testing.T) {
	s := newTestServer()
	// "secret" is gated off by default → must look unknown (no capability leak).
	out := call(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"secret","arguments":{}}}`)
	res := out["result"].(map[string]any)
	if res["isError"] != true || !strings.Contains(firstText(res), "unknown or unavailable") {
		t.Errorf("hidden tool call should be refused as unknown, got %v", res)
	}
}

func TestNotificationNoBody(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("notification status = %d, want 202", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "" {
		t.Errorf("notification should have no body, got %q", rec.Body.String())
	}
}

func TestUnknownMethod(t *testing.T) {
	s := newTestServer()
	out := call(t, s, `{"jsonrpc":"2.0","id":6,"method":"does/notexist"}`)
	e, ok := out["error"].(map[string]any)
	if !ok || e["code"].(float64) != codeMethodNotFound {
		t.Errorf("unknown method should be -32601, got %v", out["error"])
	}
}

func TestParseErrorHasNullID(t *testing.T) {
	// JSON-RPC 2.0 requires id on every response; on a parse error it must be null,
	// not omitted.
	s := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{bad json`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `"id":null`) {
		t.Errorf("parse-error response must carry \"id\":null, got %q", body)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(body), &out)
	if _, ok := out["id"]; !ok {
		t.Error("id member must be present (null) on every response")
	}
}

func TestGetRejected(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET should be 405, got %d", rec.Code)
	}
}

func toolNames(res map[string]any) map[string]bool {
	names := map[string]bool{}
	for _, ti := range res["tools"].([]any) {
		names[ti.(map[string]any)["name"].(string)] = true
	}
	return names
}

func firstText(res map[string]any) string {
	c := res["content"].([]any)
	return c[0].(map[string]any)["text"].(string)
}
