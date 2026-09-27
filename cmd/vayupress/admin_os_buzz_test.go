// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/ui"
)

// superuserPerms builds a "*:*" grant without depending on the string parser, so
// this test still means what it says if the parse format ever changes.
func superuserPerms() apikeys.Permissions {
	return apikeys.Permissions{apikeys.SectionAll: {apikeys.ActionAll: true}}
}

// TestBuzzPageCSPSafe renders the page as an operator sees it and asserts the
// VayuOS CSP contract, and that the walkthrough, sheets and reference are there.
func TestBuzzPageCSPSafe(t *testing.T) {
	out := osBuzzPage(testEndpoint, testEndpoint, false, "", nil)
	assertCSPSafe(t, "osBuzzPage", out)
	if !strings.Contains(out, testEndpoint) {
		t.Errorf("the page must show the endpoint URL %q", testEndpoint)
	}
	for _, want := range []string{"1. Grant a key", "2. Point the agent", "3. Run the agent", "4. Check it", "ADR-0146"} {
		if !strings.Contains(out, want) {
			t.Errorf("page missing %q", want)
		}
	}
	for _, id := range []string{"bz-config", "bz-about", "bz-scope"} {
		if !strings.Contains(out, `data-sheet="`+id+`"`) || !strings.Contains(out, `<dialog class="sa-sheet" id="`+id+`"`) {
			t.Errorf("sheet %q is not both opened and present", id)
		}
	}
}

// TestBuzzGrantPresets pins the capability sets. A preset that silently widened
// to a wildcard would hand an agent the whole site from a button labelled
// "author", which is the one mistake this page must never make.
func TestBuzzGrantPresets(t *testing.T) {
	out := osBuzzPage(testEndpoint, testEndpoint, false, "", nil)
	for _, preset := range []string{`data-mint="posts:read,posts:write"`, `data-mint="posts:read,analytics:read"`, `data-mint="*:*"`} {
		if !strings.Contains(out, preset) {
			t.Errorf("page missing preset %q", preset)
		}
	}
	// Every grant must be attributable to this page, or the state counts
	// nothing and an operator cannot tell a Buzz agent from any other client.
	if n := strings.Count(out, `data-label="`+buzzKeyLabelPrefix); n != 3 {
		t.Errorf("expected 3 Buzz-labelled grants, got %d", n)
	}
	// Author is the primary choice and full control is not: the safe grant
	// should be the one that looks like the default.
	if !strings.Contains(out, `class="btn btn--primary btn--sm" data-mint="posts:read,posts:write"`) {
		t.Error("author access should be the primary button")
	}
	if n := strings.Count(out, `btn--primary btn--sm" data-mint=`); n != 1 {
		t.Errorf("expected exactly one primary grant, got %d", n)
	}
}

// TestBuzzSnippetsCarryTemplate verifies the copy-paste contract: what is on
// screen before a key exists is a named placeholder, and the template the script
// rewrites carries the substitution marker. If these drift apart the page shows
// one thing and pastes another.
func TestBuzzSnippetsCarryTemplate(t *testing.T) {
	out := osBuzzPage(testEndpoint, testEndpoint, false, "", nil)
	if !strings.Contains(out, "YOUR_KEY_HERE") {
		t.Error("snippets must show a named placeholder before a key is minted")
	}
	if !strings.Contains(out, `data-tpl="`) {
		t.Error("snippets must carry a data-tpl template for the script to fill")
	}
	if !strings.Contains(out, keyTemplatePlaceholder) {
		t.Errorf("data-tpl must carry the %q marker the script substitutes", keyTemplatePlaceholder)
	}
	// Both client shapes Buzz agents actually use.
	for _, want := range []string{"claude mcp add", "mcpServers", "Authorization"} {
		if !strings.Contains(out, want) {
			t.Errorf("config sheet missing %q", want)
		}
	}
}

// TestBuzzStateCountsOnlyBuzzKeys guards the state's meaning. A key granted to
// Claude Desktop on the VayuMCP page is not a Buzz agent, and folding it in
// would tell an operator auditing agent access something untrue.
func TestBuzzStateCountsOnlyBuzzKeys(t *testing.T) {
	keys := []apikeys.Key{
		{Label: buzzKeyLabelPrefix + " (author)", Active: true},
		{Label: buzzKeyLabelPrefix + " (full control)", Active: true, Permissions: superuserPerms()},
		{Label: "VayuMCP (full control)", Active: true, Permissions: superuserPerms()},
		{Label: "Some other integration", Active: true},
	}
	out := osBuzzPage(testEndpoint, testEndpoint, false, "", keys)
	// Two Buzz keys, one of them full control: not four and two.
	if !strings.Contains(out, string(ui.State("ok", "2 Buzz agents connected"))) {
		t.Error("expected 2 Buzz agents counted")
	}
	// A full-control grant must be visibly toned, not just counted.
	if !strings.Contains(out, string(ui.State("warn", "1 with full control"))) {
		t.Error("expected 1 Buzz full-control key, in the warn tone")
	}
	if clean := osBuzzPage(testEndpoint, testEndpoint, false, "", nil); strings.Contains(clean, "with full control") {
		t.Error("an install with no Buzz keys is showing a full-control warning")
	}
}

// TestBuzzEndpointNotDoubleEscaped mirrors the VayuMCP guard: a Host carrying an
// HTML-special char must be encoded exactly once, or the operator copies a
// config that does not parse.
func TestBuzzEndpointNotDoubleEscaped(t *testing.T) {
	ep := "https://a&b.example.com/mcp"
	if out := osBuzzPage(ep, ep, false, "", nil); strings.Contains(out, "&amp;amp;") {
		t.Error("endpoint is double HTML-escaped on the page (expected single encoding)")
	}
}

// TestBuzzPageEscapesEndpoint proves the endpoint is escaped rather than
// interpolated raw. The endpoint derives from the request Host, so it is
// attacker-influenced input reaching an HTML document.
func TestBuzzPageEscapesEndpoint(t *testing.T) {
	bad := `https://x.example.com"><script>alert(1)</script>/mcp`
	out := osBuzzPage(bad, bad, false, "", nil)
	if strings.Contains(out, `"><script>alert(1)`) {
		t.Error("endpoint injected unescaped into the page")
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)") {
		t.Error("expected the injected markup to survive as escaped, inert text")
	}
}

// TestBuzzPageIsAdminGated pins the access level. This page mints API keys, so
// author or editor access to it would be a privilege escalation.
func TestBuzzPageIsAdminGated(t *testing.T) {
	if got := osPathMinLevel("/os/buzz"); got != accessAdmin {
		t.Errorf("osPathMinLevel(/os/buzz) = %d, want accessAdmin (%d)", got, accessAdmin)
	}
}
