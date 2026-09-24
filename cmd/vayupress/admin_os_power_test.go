// SPDX-License-Identifier: Apache-2.0

package main

import (
	"regexp"
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
	for _, want := range []string{"We’ll be right back", "http-equiv=\"refresh\"", "by VayuPress"} {
		if !strings.Contains(page, want) {
			t.Errorf("maintenance page missing %q", want)
		}
	}
	// Every asset the page loads, and every file its stylesheet names, must be
	// one the maintenance gate lets through, or the page renders unstyled for
	// exactly the visitors it exists for.
	for _, m := range regexp.MustCompile(`(?:href|src)="([^"?]+)`).FindAllStringSubmatch(page, -1) {
		if !maintenancePathExempt(m[1]) {
			t.Errorf("the maintenance page loads %s, which maintenance blocks", m[1])
		}
	}
	for _, m := range regexp.MustCompile(`url\("([^"]+)"\)`).FindAllStringSubmatch(repoFile(t, "static/css/vayuos.css"), -1) {
		if !strings.HasPrefix(m[1], "data:") && !maintenancePathExempt(m[1]) { // data: is inline, never fetched
			t.Errorf("the stylesheet names %s, which maintenance blocks", m[1])
		}
	}
	if !strings.Contains(page, `data-ui="still-air"`) {
		t.Error("the maintenance page does not carry the Still Air scope")
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
