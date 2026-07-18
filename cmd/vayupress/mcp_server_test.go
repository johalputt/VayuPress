package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/settings"
)

// TestProjectPublicSettingsDropsOperationalKeys is the regression test for the
// site_settings data-leak finding: the tool must expose presentational config
// only, never operational/sensitive keys like the operator's private Tor bridges
// or the VayuShield thresholds — even though GetAll returns them all.
func TestProjectPublicSettingsDropsOperationalKeys(t *testing.T) {
	all := map[string]string{
		settings.KeySiteName:      "My Site",
		settings.KeySiteTagline:   "A tagline",
		settings.KeyTorBridges:    "obfs4 1.2.3.4:443 CERT=secretbridgeline", // private, must NOT leak
		settings.KeyShieldBlock:   "0.8",                                     // operational, must NOT leak
		settings.KeyShieldRateRPM: "120",                                     // operational, must NOT leak
	}
	out := projectPublicSettings(all)

	if out[settings.KeySiteName] != "My Site" || out[settings.KeySiteTagline] != "A tagline" {
		t.Error("presentational keys (site name, tagline) must be exposed")
	}
	for _, leaked := range []string{settings.KeyTorBridges, settings.KeyShieldBlock, settings.KeyShieldRateRPM} {
		if _, present := out[leaked]; present {
			t.Errorf("operational key %q must NOT be exposed by site_settings", leaked)
		}
	}
}

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

	// Read-only posts key: read tools + site_info, but NOT create/update/delete —
	// and NOT the settings/analytics tools (those are separate sections it lacks).
	ro := listFor(scopedKey([2]string{"posts", "read"}))
	for _, want := range []string{"site_info", "get_post", "list_posts", "search_content"} {
		if !ro[want] {
			t.Errorf("read-only key should see %q", want)
		}
	}
	for _, deny := range []string{"create_post", "update_post", "delete_post", "site_settings", "analytics_summary"} {
		if ro[deny] {
			t.Errorf("posts:read key must NOT see %q (wrong section)", deny)
		}
	}

	// A settings:read key sees site_settings but no posts write/read tools.
	set := listFor(scopedKey([2]string{"settings", "read"}))
	if !set["site_settings"] {
		t.Error("settings:read key should see site_settings")
	}
	if set["analytics_summary"] || set["get_post"] {
		t.Error("settings:read key must not see other sections' tools")
	}

	// An analytics:read key sees analytics_summary and nothing cross-section.
	an := listFor(scopedKey([2]string{"analytics", "read"}))
	if !an["analytics_summary"] {
		t.Error("analytics:read key should see analytics_summary")
	}
	if an["site_settings"] || an["create_post"] {
		t.Error("analytics:read key must not see other sections' tools")
	}

	// Superuser ("full control"): every tool is visible.
	su := listFor(apikeys.SuperuserKeyInfo("s", "root", apikeys.ScopeExternal))
	for _, want := range []string{"create_post", "update_post", "delete_post", "get_post", "list_posts", "search_content", "site_info", "site_settings", "analytics_summary"} {
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
