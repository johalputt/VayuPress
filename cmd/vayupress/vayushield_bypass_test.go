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

func containsPrefix(list []string, want string) bool {
	for _, p := range list {
		if p == want {
			return true
		}
	}
	return false
}
