// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
)

// TestCSPAllowsStyleAttrNotScript pins the narrow relaxation: inline style
// ATTRIBUTES are permitted (so the admin UI stops emitting CSP-violation reports)
// while the XSS-relevant surface stays strict — scripts are nonce-locked with no
// unsafe-inline, and stylesheets/<style> stay 'self'.
func TestCSPAllowsStyleAttrNotScript(t *testing.T) {
	csp := BuildCSP("testnonce", nil)

	if !strings.Contains(csp, "style-src-attr 'unsafe-inline'") {
		t.Errorf("CSP must allow inline style attributes:\n%s", csp)
	}
	if !strings.Contains(csp, "style-src 'self'") {
		t.Errorf("stylesheets/<style> must stay 'self':\n%s", csp)
	}
	if !strings.Contains(csp, "script-src 'self' 'nonce-testnonce'") {
		t.Errorf("script-src must stay nonce-locked:\n%s", csp)
	}
	// The script surface must NEVER carry unsafe-inline — that is the XSS gate.
	scriptPart := csp
	if i := strings.Index(csp, "script-src"); i >= 0 {
		scriptPart = csp[i:]
		if j := strings.Index(scriptPart, ";"); j >= 0 {
			scriptPart = scriptPart[:j]
		}
	}
	if strings.Contains(scriptPart, "unsafe-inline") {
		t.Errorf("script-src must not allow unsafe-inline:\n%s", scriptPart)
	}
}
