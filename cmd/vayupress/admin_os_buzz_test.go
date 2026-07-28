// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
)

// superuserPerms builds a "*:*" grant without depending on the string parser, so
// this test still means what it says if the parse format ever changes.
func superuserPerms() apikeys.Permissions {
	return apikeys.Permissions{apikeys.SectionAll: {apikeys.ActionAll: true}}
}

// TestBuzzCardsCSPSafe renders every Buzz-page fragment and asserts they carry no
// inline style / unsafe-eval / external asset host (the VayuOS CSP contract), and
// that the walkthrough and grant choices are all present.
func TestBuzzCardsCSPSafe(t *testing.T) {
	endpoint := "https://blog.example.com/mcp"

	intro := osBuzzIntro()
	assertCSPSafe(t, "osBuzzIntro", intro)

	setup := osBuzzSetupCards(endpoint)
	assertCSPSafe(t, "osBuzzSetupCards", setup)
	if !strings.Contains(setup, endpoint) {
		t.Errorf("setup cards must show the endpoint URL %q", endpoint)
	}

	grant := osBuzzGrantCard()
	assertCSPSafe(t, "osBuzzGrantCard", grant)

	about := osBuzzAboutCard()
	assertCSPSafe(t, "osBuzzAboutCard", about)

	stats := osBuzzStats(endpoint, nil, false, "")
	assertCSPSafe(t, "osBuzzStats", stats)
}

// TestBuzzGrantPresets pins the capability sets. A preset that silently widened
// to a wildcard would hand an agent the whole site from a button labelled
// "author", which is the one mistake this page must never make.
func TestBuzzGrantPresets(t *testing.T) {
	grant := osBuzzGrantCard()
	for _, preset := range []string{
		`data-mint="posts:read,posts:write"`,
		`data-mint="posts:read,analytics:read"`,
		`data-mint="*:*"`,
	} {
		if !strings.Contains(grant, preset) {
			t.Errorf("grant card missing preset %q", preset)
		}
	}
	// Every grant must be attributable to this page, or the stat strip counts
	// nothing and an operator cannot tell a Buzz agent from any other client.
	if n := strings.Count(grant, `data-label="`+buzzKeyLabelPrefix); n != 3 {
		t.Errorf("expected 3 Buzz-labelled grants, got %d", n)
	}
	// Author is the primary (btn--primary) choice and full control is not: the
	// safe grant should be the one that looks like the default.
	if !strings.Contains(grant, `class="btn btn--primary" data-mint="posts:read,posts:write"`) {
		t.Error("author access should be the primary button")
	}
	if strings.Contains(grant, `class="btn btn--primary" data-mint="*:*"`) {
		t.Error("full control must not be styled as the primary/default choice")
	}
}

// TestBuzzSnippetsCarryTemplate verifies the copy-paste contract: what is on
// screen before a key exists is a named placeholder, and the template the script
// rewrites carries the substitution marker. If these drift apart the page shows
// one thing and pastes another.
func TestBuzzSnippetsCarryTemplate(t *testing.T) {
	out := osBuzzSetupCards("https://blog.example.com/mcp")

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
			t.Errorf("setup cards missing %q", want)
		}
	}
	// The script's substitution must target the same marker the HTML emits.
	if !strings.Contains(mcpClientScript, keyTemplatePlaceholder) {
		t.Error("page script does not reference the placeholder it is meant to replace")
	}
	if !strings.Contains(mcpClientScript, "data-tpl") {
		t.Error("page script does not select the snippets it is meant to fill")
	}
}

// TestBuzzStatsCountsOnlyBuzzKeys guards the stat strip's meaning. A key granted
// to Claude Desktop on the VayuMCP page is not a Buzz agent, and folding it in
// would tell an operator auditing agent access something untrue.
func TestBuzzStatsCountsOnlyBuzzKeys(t *testing.T) {
	keys := []apikeys.Key{
		{Label: buzzKeyLabelPrefix + " (author)", Active: true},
		{Label: buzzKeyLabelPrefix + " (full control)", Active: true, Permissions: superuserPerms()},
		{Label: "VayuMCP (full control)", Active: true, Permissions: superuserPerms()},
		{Label: "Some other integration", Active: true},
	}
	out := osBuzzStats("https://blog.example.com/mcp", keys, false, "")

	// Two Buzz keys, one of them full control — not four and two.
	if !strings.Contains(out, `<div class="stat-card__value">2</div>`) {
		t.Errorf("expected 2 Buzz agents counted, got:\n%s", out)
	}
	if !strings.Contains(out, `<div class="stat-card__value">1</div>`) {
		t.Errorf("expected 1 Buzz full-control key counted, got:\n%s", out)
	}
	// A full-control grant must be visibly toned, not just counted.
	if !strings.Contains(out, "stat-card--warn") {
		t.Error("a full-control Buzz key should raise the warn tone on its tile")
	}
}

// TestBuzzStatsHostTone covers the three endpoint-host states, because each one
// asks the operator for a different action (nothing / nothing / fix your proxy).
func TestBuzzStatsHostTone(t *testing.T) {
	main := osBuzzStats("https://blog.example.com/mcp", nil, false, "")
	if !strings.Contains(main, "Main domain") {
		t.Error("plain install should report the main domain")
	}
	if strings.Contains(main, "stat-card--warn") {
		t.Error("a healthy install with no full-control keys must not show a warning tone")
	}

	dedicated := osBuzzStats("https://mcp.example.com/mcp", nil, true, "")
	if !strings.Contains(dedicated, "Dedicated host") {
		t.Error("dedicated host should be reported as such")
	}

	blocked := osBuzzStats("https://blog.example.com/mcp", nil, false, "mcp.example.com")
	if !strings.Contains(blocked, "Dedicated host blocked") {
		t.Error("a challenged dedicated host must be named distinctly from 'not set up'")
	}
	if !strings.Contains(blocked, "stat-card--warn") {
		t.Error("a blocked dedicated host should carry the warn tone")
	}
}

// TestBuzzEndpointNotDoubleEscaped mirrors the VayuMCP guard: a Host carrying an
// HTML-special char must be encoded exactly once, or the operator copies a
// config that does not parse.
func TestBuzzEndpointNotDoubleEscaped(t *testing.T) {
	out := osBuzzSetupCards("https://a&b.example.com/mcp")
	if strings.Contains(out, "&amp;amp;") {
		t.Error("endpoint is double HTML-escaped in the setup cards (expected single encoding)")
	}
}

// TestBuzzSetupCardsEscapeEndpoint proves the endpoint is escaped rather than
// interpolated raw. The endpoint derives from the request Host, so it is
// attacker-influenced input reaching an HTML document.
func TestBuzzSetupCardsEscapeEndpoint(t *testing.T) {
	out := osBuzzSetupCards(`https://x.example.com"><script>alert(1)</script>/mcp`)
	if strings.Contains(out, `"><script>alert(1)`) {
		t.Error("endpoint injected unescaped into the setup cards")
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
