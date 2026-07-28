// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
)

// TestClaudeCodeCardsCSPSafe renders every fragment and asserts the VayuOS CSP
// contract holds, and that all three routes in are present.
func TestClaudeCodeCardsCSPSafe(t *testing.T) {
	endpoint := "https://blog.example.com/mcp"

	intro := osClaudeCodeIntro()
	assertCSPSafe(t, "osClaudeCodeIntro", intro)

	setup := osClaudeCodeSetupCards(endpoint, endpoint, false, "")
	assertCSPSafe(t, "osClaudeCodeSetupCards", setup)
	if !strings.Contains(setup, endpoint) {
		t.Errorf("setup cards must show the endpoint URL %q", endpoint)
	}
	// All three routes in — one-click, CLI, Desktop.
	for _, want := range []string{"Add custom connector", "claude mcp add", "claude_desktop_config.json"} {
		if !strings.Contains(setup, want) {
			t.Errorf("setup cards missing %q", want)
		}
	}

	grant := osClaudeCodeGrantCard()
	assertCSPSafe(t, "osClaudeCodeGrantCard", grant)

	trouble := osClaudeCodeTroubleshootCard()
	assertCSPSafe(t, "osClaudeCodeTroubleshootCard", trouble)
	// The WAF expression is the actionable payload of that card.
	for _, want := range []string{"/mcp", "/oauth/", "/.well-known/"} {
		if !strings.Contains(trouble, want) {
			t.Errorf("troubleshoot card missing the path %q to exempt", want)
		}
	}

	stats := osClaudeCodeStats(endpoint, nil, false, "")
	assertCSPSafe(t, "osClaudeCodeStats", stats)
}

// TestClaudeCodeOneClickLeads pins the ordering decision. The one-click route
// needs no key, so there is no token to leak, paste wrongly or forget to revoke —
// it must be the open, recommended accordion, and it must not be demoted by a
// later edit.
func TestClaudeCodeOneClickLeads(t *testing.T) {
	setup := osClaudeCodeSetupCards("https://blog.example.com/mcp", "https://blog.example.com/mcp", false, "")

	oneClick := strings.Index(setup, "One-click Connect")
	cli := strings.Index(setup, "Claude Code (CLI)")
	desktop := strings.Index(setup, "Claude Desktop (config file)")
	if oneClick < 0 || cli < 0 || desktop < 0 {
		t.Fatalf("expected all three routes; got indexes %d/%d/%d", oneClick, cli, desktop)
	}
	if !(oneClick < cli && cli < desktop) {
		t.Error("one-click must come first, then CLI, then Desktop")
	}
	if !strings.Contains(setup, `● Recommended`) {
		t.Error("the keyless route should be marked Recommended")
	}
	// Exactly one accordion opens by default, and it is the one-click one.
	if n := strings.Count(setup, "<details class=\"mon-acc\" open>"); n != 1 {
		t.Errorf("expected exactly 1 open accordion, got %d", n)
	}
	if idx := strings.Index(setup, "<details class=\"mon-acc\" open>"); idx > oneClick {
		t.Error("the open accordion is not the one-click route")
	}
}

// TestClaudeCodeGrantPresets pins the capability sets and the label convention.
func TestClaudeCodeGrantPresets(t *testing.T) {
	grant := osClaudeCodeGrantCard()
	for _, preset := range []string{
		`data-mint="*:*"`,
		`data-mint="posts:read,posts:write"`,
		`data-mint="posts:read,analytics:read"`,
	} {
		if !strings.Contains(grant, preset) {
			t.Errorf("grant card missing preset %q", preset)
		}
	}
	if n := strings.Count(grant, `data-label="`+claudeKeyLabelPrefix); n != 3 {
		t.Errorf("expected 3 Claude-labelled grants, got %d", n)
	}
	// This page's common case is an operator connecting their own assistant to
	// their own site, so full control leads here — the opposite of the Buzz page,
	// deliberately. Pinning both stops one being "fixed" to match the other.
	if !strings.Contains(grant, `class="btn btn--primary" data-mint="*:*"`) {
		t.Error("full control should be the primary button on the Claude page")
	}
}

// TestClaudeAndBuzzKeysDoNotCrossCount is the guard that makes either stat strip
// meaningful: each page counts only the keys it minted.
func TestClaudeAndBuzzKeysDoNotCrossCount(t *testing.T) {
	keys := []apikeys.Key{
		{Label: claudeKeyLabelPrefix + " (full control)", Active: true, Permissions: superuserPerms()},
		{Label: claudeKeyLabelPrefix + " (author)", Active: true},
		{Label: buzzKeyLabelPrefix + " (author)", Active: true},
		{Label: "Some other integration", Active: true},
	}
	claude := osClaudeCodeStats("https://blog.example.com/mcp", keys, false, "")
	buzz := osBuzzStats("https://blog.example.com/mcp", keys, false, "")

	if !strings.Contains(claude, `<div class="stat-card__value">2</div>`) {
		t.Errorf("Claude page should count 2 Claude clients, got:\n%s", claude)
	}
	if !strings.Contains(buzz, `<div class="stat-card__value">1</div>`) {
		t.Errorf("Buzz page should count 1 Buzz agent, got:\n%s", buzz)
	}
	// The Claude full-control key must not raise a warning on the Buzz page.
	if strings.Contains(buzz, "stat-card--warn") {
		t.Error("a Claude full-control key must not tone the Buzz page's tiles")
	}
}

// TestClaudeCodeSnippetsCarryTemplate verifies the copy-paste contract.
func TestClaudeCodeSnippetsCarryTemplate(t *testing.T) {
	out := osClaudeCodeSetupCards("https://blog.example.com/mcp", "https://blog.example.com/mcp", false, "")
	if !strings.Contains(out, "YOUR_KEY_HERE") {
		t.Error("snippets must show a named placeholder before a key is minted")
	}
	if !strings.Contains(out, keyTemplatePlaceholder) {
		t.Errorf("data-tpl must carry the %q marker the script substitutes", keyTemplatePlaceholder)
	}
	if !strings.Contains(mcpClientScript, keyTemplatePlaceholder) || !strings.Contains(mcpClientScript, "data-tpl") {
		t.Error("shared script does not fill the snippets it is meant to")
	}
}

// TestClaudeCodeEndpointNotDoubleEscaped mirrors the VayuMCP guard.
func TestClaudeCodeEndpointNotDoubleEscaped(t *testing.T) {
	out := osClaudeCodeSetupCards("https://a&b.example.com/mcp", "https://a&b.example.com/mcp", false, "")
	if strings.Contains(out, "&amp;amp;") {
		t.Error("endpoint is double HTML-escaped in the setup cards")
	}
}

// TestClaudeCodeSetupCardsEscapeEndpoint — the endpoint derives from the request
// Host, so it is attacker-influenced input reaching an HTML document.
func TestClaudeCodeSetupCardsEscapeEndpoint(t *testing.T) {
	bad := `https://x.example.com"><script>alert(1)</script>/mcp`
	out := osClaudeCodeSetupCards(bad, bad, false, "")
	if strings.Contains(out, `"><script>alert(1)`) {
		t.Error("endpoint injected unescaped into the setup cards")
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)") {
		t.Error("expected the injected markup to survive as escaped, inert text")
	}
}

// TestClaudeCodeHostNote covers the dedicated-host states, which ask the operator
// for different actions (nothing vs. fix your proxy).
func TestClaudeCodeHostNote(t *testing.T) {
	dedicated := osClaudeCodeSetupCards("https://mcp.example.com/mcp", "https://blog.example.com/mcp", true, "")
	if !strings.Contains(dedicated, "dedicated host") {
		t.Error("a dedicated host should be explained, or the differing URL reads as a bug")
	}
	if !strings.Contains(dedicated, "blog.example.com") {
		t.Error("the dedicated-host note must name the URL it replaced")
	}

	blocked := osClaudeCodeSetupCards("https://blog.example.com/mcp", "https://blog.example.com/mcp", false, "mcp.example.com")
	if !strings.Contains(blocked, "blocked") {
		t.Error("a challenged dedicated host must be named distinctly from 'not set up'")
	}
}

// TestClaudeCodePageIsAdminGated — this page mints API keys, so author or editor
// access to it would be a privilege escalation.
func TestClaudeCodePageIsAdminGated(t *testing.T) {
	if got := osPathMinLevel("/os/claudecode"); got != accessAdmin {
		t.Errorf("osPathMinLevel(/os/claudecode) = %d, want accessAdmin (%d)", got, accessAdmin)
	}
}
