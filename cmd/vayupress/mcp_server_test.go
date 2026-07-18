package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
)

// keyCtxRequest builds an MCP POST request carrying ki in context, exactly as
// RequireAPIKey would stamp it after authenticating.
func keyCtxRequest(body string, ki apikeys.KeyInfo) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	return auth.RequestWithKeyInfo(req, ki)
}

func scopedKey(grants ...[2]string) apikeys.KeyInfo {
	p := apikeys.NewPermissions()
	for _, g := range grants {
		p.Grant(apikeys.Section(g[0]), apikeys.Action(g[1]))
	}
	return apikeys.KeyInfo{ID: "k1", Label: "scoped", Scope: apikeys.ScopeExternal, Perms: p}
}

func TestMCPVisibleGating(t *testing.T) {
	app := &App{}
	writable := app.mcpVisible(apikeys.SectionPosts, apikeys.ActionWrite)
	readable := app.mcpVisible(apikeys.SectionPosts, apikeys.ActionRead)

	// No key in context → nothing is visible.
	if writable(httptest.NewRequest("POST", "/mcp", nil).Context()) {
		t.Error("no key must not grant posts:write")
	}
	// posts:read key → read yes, write no.
	rk := scopedKey([2]string{"posts", "read"})
	rctx := auth.RequestWithKeyInfo(httptest.NewRequest("POST", "/mcp", nil), rk).Context()
	if !readable(rctx) {
		t.Error("posts:read key must grant read")
	}
	if writable(rctx) {
		t.Error("posts:read key must NOT grant write")
	}
	// Superuser → everything.
	sctx := auth.RequestWithKeyInfo(httptest.NewRequest("POST", "/mcp", nil),
		apikeys.SuperuserKeyInfo("s", "root", apikeys.ScopeExternal)).Context()
	if !writable(sctx) || !readable(sctx) {
		t.Error("superuser key must grant all posts actions")
	}
}

// TestMCPToolsListReflectsScope is the end-to-end security proof: the connector's
// visible tool surface equals the key's grant. A read-only key must not even see
// the write/delete tools; a superuser sees the full set.
func TestMCPToolsListReflectsScope(t *testing.T) {
	srv := (&App{}).buildMCPServer()

	listFor := func(ki apikeys.KeyInfo) map[string]bool {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, keyCtxRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, ki))
		var out struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("bad tools/list response: %v", err)
		}
		names := map[string]bool{}
		for _, tl := range out.Result.Tools {
			names[tl.Name] = true
		}
		return names
	}

	// Read-only posts key: read tools + site_info, but NOT create/update/delete.
	ro := listFor(scopedKey([2]string{"posts", "read"}))
	for _, want := range []string{"site_info", "get_post", "list_posts", "search_content"} {
		if !ro[want] {
			t.Errorf("read-only key should see %q", want)
		}
	}
	for _, deny := range []string{"create_post", "update_post", "delete_post"} {
		if ro[deny] {
			t.Errorf("read-only key must NOT see %q", deny)
		}
	}

	// Superuser ("full control"): every tool is visible.
	su := listFor(apikeys.SuperuserKeyInfo("s", "root", apikeys.ScopeExternal))
	for _, want := range []string{"create_post", "update_post", "delete_post", "get_post", "list_posts", "search_content", "site_info"} {
		if !su[want] {
			t.Errorf("superuser (full control) should see %q", want)
		}
	}

	// A key with no grants sees only the ungated site_info probe.
	none := listFor(apikeys.KeyInfo{Scope: apikeys.ScopeExternal, Perms: apikeys.NewPermissions()})
	if none["create_post"] || none["get_post"] {
		t.Error("a no-grant key must not see any posts tools")
	}
	if !none["site_info"] {
		t.Error("site_info is ungated and should always be visible")
	}
}
