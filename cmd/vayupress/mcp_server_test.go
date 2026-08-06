// SPDX-License-Identifier: Apache-2.0

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

// TestPartitionAllowedSettings proves the write-side guard: update_site_settings
// applies only presentational keys and ignores operational ones (tor.bridges,
// shield.*) even when the caller requests them.
func TestPartitionAllowedSettings(t *testing.T) {
	apply, ignored := partitionAllowedSettings(map[string]string{
		settings.KeySiteName:    "New Name",
		settings.KeyNavItems:    `[{"label":"Home","href":"/"}]`,
		settings.KeyTorBridges:  "obfs4 1.2.3.4:443 CERT=x", // must be ignored
		settings.KeyShieldBlock: "0.9",                      // must be ignored
		"totally.unknown.key":   "x",                        // must be ignored
	})
	if apply[settings.KeySiteName] != "New Name" || apply[settings.KeyNavItems] == "" {
		t.Error("presentational keys must be applied")
	}
	for _, k := range []string{settings.KeyTorBridges, settings.KeyShieldBlock, "totally.unknown.key"} {
		if _, ok := apply[k]; ok {
			t.Errorf("operational/unknown key %q must NOT be applied", k)
		}
	}
	ignoredSet := map[string]bool{}
	for _, k := range ignored {
		ignoredSet[k] = true
	}
	if !ignoredSet[settings.KeyTorBridges] || !ignoredSet[settings.KeyShieldBlock] {
		t.Error("rejected operational keys should be reported in ignored")
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
	for _, want := range []string{"site_info", "get_post", "list_posts", "search_content", "list_pages"} {
		if !ro[want] {
			t.Errorf("read-only key should see %q", want)
		}
	}
	for _, deny := range []string{"create_post", "update_post", "delete_post", "create_page", "site_settings", "analytics_summary"} {
		if ro[deny] {
			t.Errorf("posts:read key must NOT see %q (wrong section/action)", deny)
		}
	}

	// upload_media writes to the media directory, so a media:read key must not
	// see it. The scoping is the only thing standing between "this key can list
	// my images" and "this key can put files on my origin".
	med := listFor(scopedKey([2]string{"media", "read"}))
	if !med["list_media"] {
		t.Error("media:read key should see list_media")
	}
	if med["upload_media"] {
		t.Error("media:read key must NOT see upload_media — reading the library and writing " +
			"to it are different permissions, and this one writes files that get served from " +
			"our own origin")
	}

	// A settings:read key sees site_settings but NOT the write tool nor other
	// sections' tools.
	set := listFor(scopedKey([2]string{"settings", "read"}))
	if !set["site_settings"] {
		t.Error("settings:read key should see site_settings")
	}
	if set["update_site_settings"] {
		t.Error("settings:read key must NOT see update_site_settings (needs settings:write)")
	}
	if set["analytics_summary"] || set["get_post"] {
		t.Error("settings:read key must not see other sections' tools")
	}

	// A settings:write key sees the write tool (and, since write does not imply
	// read, not necessarily site_settings — that is by design).
	setW := listFor(scopedKey([2]string{"settings", "write"}))
	if !setW["update_site_settings"] {
		t.Error("settings:write key should see update_site_settings")
	}

	// An analytics:read key sees analytics_summary and nothing cross-section.
	an := listFor(scopedKey([2]string{"analytics", "read"}))
	if !an["analytics_summary"] {
		t.Error("analytics:read key should see analytics_summary")
	}
	if an["site_settings"] || an["create_post"] {
		t.Error("analytics:read key must not see other sections' tools")
	}

	// A media:read key sees list_media only.
	md := listFor(scopedKey([2]string{"media", "read"}))
	if !md["list_media"] {
		t.Error("media:read key should see list_media")
	}
	if md["list_themes"] || md["create_post"] {
		t.Error("media:read key must not see other sections' tools")
	}

	// themes:read sees list_themes but NOT apply_theme (that needs themes:apply);
	// themes:apply sees apply_theme.
	thR := listFor(scopedKey([2]string{"themes", "read"}))
	if !thR["list_themes"] {
		t.Error("themes:read key should see list_themes")
	}
	if thR["apply_theme"] {
		t.Error("themes:read key must NOT see apply_theme (needs themes:apply)")
	}
	thA := listFor(scopedKey([2]string{"themes", "apply"}))
	if !thA["apply_theme"] {
		t.Error("themes:apply key should see apply_theme")
	}

	// Superuser ("full control"): every tool is visible.
	su := listFor(apikeys.SuperuserKeyInfo("s", "root", apikeys.ScopeExternal))
	for _, want := range []string{"create_post", "update_post", "delete_post", "get_post", "list_posts", "search_content", "create_page", "list_pages", "site_info", "site_settings", "update_site_settings", "analytics_summary", "list_media", "list_themes", "apply_theme"} {
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

// TestSiteSettingsProjectionKeepsTheResponseUsable is the regression test for a
// tool that was correct about secrecy and useless in practice.
//
// theme.og_image stores the share image as raw base64 — routinely over a
// megabyte. The projection copied it verbatim, so a single site_settings
// response ran to ~1.9 MB, of which 99.98% was that one value, and clients could
// not read the result at all. Nothing leaked; the tool simply could not do the
// job its own description claims ("use it to understand the current site").
//
// The renderer had already solved this — OGImagePath maps the stored blob to its
// public path so the page links the image instead of inlining it. Size is part of
// the contract here, not only sensitivity.
func TestSiteSettingsProjectionKeepsTheResponseUsable(t *testing.T) {
	big := strings.Repeat("A", 1_500_000) // a realistic share image, base64
	all := map[string]string{
		settings.KeySiteName:     "Johal",
		settings.KeyThemeOGImage: big,
		"tor.bridges":            "obfs4 10.0.0.1:443 SECRET",
		"shield.block_threshold": "80",
	}

	out := projectPublicSettings(all)

	if got := out[settings.KeyThemeOGImage]; strings.Contains(got, "AAAA") {
		t.Errorf("the raw image blob is still in the response (%d bytes) — no client can read this", len(got))
	}
	if got := out[settings.KeyThemeOGImage]; got != "/theme-assets/og" {
		t.Errorf("og_image projected to %q, want the public path", got)
	}
	// An absent image must project to empty, not to a path that serves nothing.
	if got := projectPublicSettings(map[string]string{settings.KeyThemeOGImage: ""}); got[settings.KeyThemeOGImage] != "" {
		t.Errorf("no image set, but the projection advertised %q", got[settings.KeyThemeOGImage])
	}
	// The whole response must stay small enough to actually return.
	total := 0
	for k, v := range out {
		total += len(k) + len(v)
	}
	if total > 100_000 {
		t.Errorf("projected settings are %d bytes; the tool is unusable at this size", total)
	}
	// The original secrecy guarantee must still hold.
	for _, k := range []string{"tor.bridges", "shield.block_threshold"} {
		if _, leaked := out[k]; leaked {
			t.Errorf("operational key %q reached a connector", k)
		}
	}
}

// TestOGImageIsNotWritableThroughTheTool — the read side returns a PATH, so a
// client that reads the settings map, edits one field and writes it back (the
// most natural way to use the pair) would otherwise store "/theme-assets/og" as
// the image data and destroy it.
func TestOGImageIsNotWritableThroughTheTool(t *testing.T) {
	apply, ignored := partitionAllowedSettings(map[string]string{
		settings.KeySiteName:     "Johal",
		settings.KeyThemeOGImage: "/theme-assets/og",
	})
	if _, ok := apply[settings.KeyThemeOGImage]; ok {
		t.Error("og_image is writable: a read-modify-write round trip would overwrite the image with its own URL")
	}
	if apply[settings.KeySiteName] != "Johal" {
		t.Error("blocking og_image also blocked an ordinary writable key")
	}
	found := false
	for _, k := range ignored {
		if k == settings.KeyThemeOGImage {
			found = true
		}
	}
	if !found {
		t.Error("the refusal must be reported to the caller, not silent")
	}
}
