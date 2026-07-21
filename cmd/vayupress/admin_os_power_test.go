package main

import (
	"strings"
	"testing"
)

// TestMaintenancePageHTML checks the public maintenance page is a self-contained
// premium page (no external CSS/JS that maintenance would block) and that a
// custom operator message is HTML-escaped, not injected raw.
func TestMaintenancePageHTML(t *testing.T) {
	page := maintenancePageHTML(`<script>alert(1)</script>`)
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Error("operator message must be HTML-escaped, not injected raw")
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Error("expected the message to be escaped")
	}
	for _, want := range []string{"We’ll be right back", "http-equiv=\"refresh\"", "powered by VayuPress"} {
		if !strings.Contains(page, want) {
			t.Errorf("maintenance page missing %q", want)
		}
	}
	// Self-contained: no external stylesheet/script the maintenance gate would block.
	if strings.Contains(page, "/static/") || strings.Contains(page, "/theme.css") || strings.Contains(page, "<script src") {
		t.Error("maintenance page must be fully self-contained")
	}
	// Default (empty) message falls back to the friendly copy.
	if !strings.Contains(maintenancePageHTML(""), "back online shortly") {
		t.Error("empty message should use the default copy")
	}
}

// TestMaintenancePathExempt is the safety guarantee: while the public site is in
// maintenance, the VayuOS console (including the LOGIN page and its assets), the
// health probes and the operational surfaces stay reachable — so the operator
// can always sign in and turn maintenance back off, and can never be locked out.
func TestMaintenancePathExempt(t *testing.T) {
	// Must stay reachable during maintenance.
	for _, p := range []string{
		"/os", "/os/login", "/os/power", "/os/power/preview",
		"/os/static/js/admin-os.js", "/os/api/power/maintenance",
		"/health", "/health/ready", "/__vayushield/pow",
		"/.well-known/acme-challenge/x", "/mcp", "/oauth/token", "/favicon.ico",
	} {
		if !maintenancePathExempt(p) {
			t.Errorf("path %q MUST stay reachable during maintenance (operator lockout risk)", p)
		}
	}
	// Public paths get the maintenance page. Note "/osborne" starts with "/os"
	// but is NOT the console — the match must be segment-aware.
	for _, p := range []string{"/", "/blog", "/about", "/some-post", "/osborne", "/oscars"} {
		if maintenancePathExempt(p) {
			t.Errorf("public path %q should show the maintenance page, not bypass it", p)
		}
	}
}
