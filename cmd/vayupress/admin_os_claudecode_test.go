// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/ui"
)

const testEndpoint = "https://blog.example.com/mcp"

// TestClaudeCodePageCSPSafe renders the page as an operator sees it and asserts
// the VayuOS CSP contract holds, and that all three routes in are present.
func TestClaudeCodePageCSPSafe(t *testing.T) {
	out := osClaudeCodePage(testEndpoint, testEndpoint, false, "", nil)
	assertCSPSafe(t, "osClaudeCodePage", out)
	if !strings.Contains(out, testEndpoint) {
		t.Errorf("the page must show the endpoint URL %q", testEndpoint)
	}
	// All three routes in: one-click, CLI, Desktop.
	for _, want := range []string{"Add custom connector", "claude mcp add", "claude_desktop_config.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The WAF expression is the actionable payload of the proxy sheet.
	for _, want := range []string{`"/mcp"`, `"/oauth/"`, `"/.well-known/"`} {
		if !strings.Contains(out, want) {
			t.Errorf("proxy sheet missing the path %s to exempt", want)
		}
	}
	// Every sheet a row opens must exist, or the button does nothing.
	for _, id := range []string{"cc-cli", "cc-desktop", "cc-proxy"} {
		if !strings.Contains(out, `data-sheet="`+id+`"`) || !strings.Contains(out, `<dialog class="sa-sheet" id="`+id+`"`) {
			t.Errorf("sheet %q is not both opened and present", id)
		}
	}
}

// TestClaudeCodeOneClickLeads pins the ordering decision. The one-click route
// needs no key, so there is no token to leak, paste wrongly or forget to revoke:
// it comes first and is the only route marked Recommended.
func TestClaudeCodeOneClickLeads(t *testing.T) {
	out := osClaudeCodePage(testEndpoint, testEndpoint, false, "", nil)
	oneClick := strings.Index(out, "One-click Connect")
	cli := strings.Index(out, `data-sheet="cc-cli"`)
	desktop := strings.Index(out, `data-sheet="cc-desktop"`)
	if oneClick < 0 || cli < 0 || desktop < 0 {
		t.Fatalf("expected all three routes; got indexes %d/%d/%d", oneClick, cli, desktop)
	}
	if !(oneClick < cli && cli < desktop) {
		t.Error("one-click must come first, then Claude Code, then Desktop")
	}
	rec := string(ui.State("ok", "Recommended"))
	if n := strings.Count(out, rec); n != 1 {
		t.Errorf("expected exactly one route marked Recommended, got %d", n)
	}
	if idx := strings.Index(out, rec); idx < oneClick || idx > cli {
		t.Error("the Recommended mark is not on the one-click route")
	}
}

// TestClaudeCodeGrantPresets pins the capability sets and the label convention.
func TestClaudeCodeGrantPresets(t *testing.T) {
	out := osClaudeCodePage(testEndpoint, testEndpoint, false, "", nil)
	for _, preset := range []string{`data-mint="*:*"`, `data-mint="posts:read,posts:write"`, `data-mint="posts:read,analytics:read"`} {
		if !strings.Contains(out, preset) {
			t.Errorf("page missing preset %q", preset)
		}
	}
	if n := strings.Count(out, `data-label="`+claudeKeyLabelPrefix); n != 3 {
		t.Errorf("expected 3 Claude-labelled grants, got %d", n)
	}
	// This page's common case is an operator connecting their own assistant to
	// their own site, so full control leads here, the opposite of the Buzz page,
	// deliberately. Pinning both stops one being "fixed" to match the other.
	if !strings.Contains(out, `class="btn btn--primary btn--sm" data-mint="*:*"`) {
		t.Error("full control should be the primary button on the Claude page")
	}
	if n := strings.Count(out, `btn--primary btn--sm" data-mint=`); n != 1 {
		t.Errorf("expected exactly one primary grant, got %d", n)
	}
}

// TestClaudeAndBuzzKeysDoNotCrossCount is the guard that makes either page's
// state meaningful: each page counts only the keys it minted.
func TestClaudeAndBuzzKeysDoNotCrossCount(t *testing.T) {
	keys := []apikeys.Key{
		{Label: claudeKeyLabelPrefix + " (full control)", Active: true, Permissions: superuserPerms()},
		{Label: claudeKeyLabelPrefix + " (author)", Active: true},
		{Label: buzzKeyLabelPrefix + " (author)", Active: true},
		{Label: "Some other integration", Active: true},
	}
	claude := osClaudeCodePage(testEndpoint, testEndpoint, false, "", keys)
	buzz := osBuzzPage(testEndpoint, testEndpoint, false, "", keys)

	if !strings.Contains(claude, string(ui.State("ok", "2 Claude clients connected"))) {
		t.Error("the Claude page should count 2 Claude clients")
	}
	if !strings.Contains(claude, string(ui.State("warn", "1 with full control"))) {
		t.Error("the Claude page should warn of its full-control key")
	}
	if !strings.Contains(buzz, string(ui.State("ok", "1 Buzz agent connected"))) {
		t.Error("the Buzz page should count 1 Buzz agent")
	}
	// The Claude full-control key must not raise a warning on the Buzz page.
	if strings.Contains(buzz, "with full control") {
		t.Error("a Claude full-control key must not tone the Buzz page's state")
	}
}

// TestClaudeCodeSnippetsCarryTemplate verifies the copy-paste contract.
func TestClaudeCodeSnippetsCarryTemplate(t *testing.T) {
	out := osClaudeCodePage(testEndpoint, testEndpoint, false, "", nil)
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
	ep := "https://a&b.example.com/mcp"
	out := osClaudeCodePage(ep, ep, false, "", nil)
	if strings.Contains(out, "&amp;amp;") {
		t.Error("endpoint is double HTML-escaped on the page")
	}
}

// TestClaudeCodePageEscapesEndpoint — the endpoint derives from the request
// Host, so it is attacker-influenced input reaching an HTML document.
func TestClaudeCodePageEscapesEndpoint(t *testing.T) {
	bad := `https://x.example.com"><script>alert(1)</script>/mcp`
	out := osClaudeCodePage(bad, bad, false, "", nil)
	if strings.Contains(out, `"><script>alert(1)`) {
		t.Error("endpoint injected unescaped into the page")
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)") {
		t.Error("expected the injected markup to survive as escaped, inert text")
	}
}

// TestClaudeCodeHostNote covers the dedicated-host states, which ask the operator
// for different actions (nothing vs. fix your proxy).
func TestClaudeCodeHostNote(t *testing.T) {
	dedicated := osClaudeCodePage("https://mcp.example.com/mcp", testEndpoint, true, "", nil)
	if !strings.Contains(dedicated, string(ui.State("ok", "Dedicated host"))) {
		t.Error("a dedicated host should be named, or the differing URL reads as a bug")
	}
	if !strings.Contains(dedicated, "instead of "+testEndpoint) {
		t.Error("the dedicated-host note must name the URL it replaced")
	}

	blocked := osClaudeCodePage(testEndpoint, testEndpoint, false, "mcp.example.com", nil)
	if !strings.Contains(blocked, string(ui.State("warn", "Dedicated host blocked"))) {
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
