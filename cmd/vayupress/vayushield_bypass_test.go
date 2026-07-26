// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// TestMachineProtocolPrefixesBypassShield locks in the fix for the Claude Desktop
// "Couldn't register with johal.in's sign-in service" failure: VayuShield and the
// sovereignty lane must never challenge or shed the machine-protocol surfaces.
// Their callers — MCP clients and Anthropic's OAuth backend — cannot solve a
// browser proof-of-work/JS challenge, so a challenge on /oauth/register or /mcp is
// indistinguishable from an outage (discovery via /.well-known would succeed while
// dynamic client registration silently fails). Both endpoints carry their own
// defences (API-key + per-key rate budget on /mcp; discovery rate limit and an
// admin-session requirement on /oauth), so bypassing bot protection is safe.
func TestMachineProtocolPrefixesBypassShield(t *testing.T) {
	for _, want := range []string{"/mcp", "/oauth"} {
		if !containsPrefix(shieldBypassPrefixes, want) {
			t.Errorf("shieldBypassPrefixes must contain %q (else Claude cannot connect the MCP connector)", want)
		}
		if !containsPrefix(sovereignPrefixes, want) {
			t.Errorf("sovereignPrefixes must contain %q (else machine traffic is shed with the public pool)", want)
		}
	}
}

// TestInstallSurfacesBypassShield locks in the other half of "I installed the app
// and a restart deleted it".
//
// The manifest is not fetched only by the reader's browser: the WebAPK minting
// server downloads it, and the icons it names, to build the installable package,
// and re-downloads it on every update check. Those fetches do not come from a
// mainstream browser and cannot solve a proof-of-work or JS challenge, so a
// challenge on /manifest.json means no WebAPK gets minted — and Chrome quietly
// substitutes a launcher shortcut, which many Android builds drop on reboot. A
// challenge on /sw.js is the same shape of failure: the browser asks for
// JavaScript and is handed an HTML challenge page, so the worker update fails on an
// app that is already installed.
func TestInstallSurfacesBypassShield(t *testing.T) {
	for _, want := range []string{"/manifest.json", "/sw.js"} {
		if !containsPrefix(shieldBypassPrefixes, want) {
			t.Errorf("shieldBypassPrefixes must contain %q (else the site cannot be installed as a real app)", want)
		}
	}
	// The icons the manifest names must be reachable on the same terms; they live
	// under /static, which is already bypassed.
	if !containsPrefix(shieldBypassPrefixes, "/static") {
		t.Error("shieldBypassPrefixes must contain /static so the app icons are fetchable")
	}
}
