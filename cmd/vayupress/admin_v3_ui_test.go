package main

import (
	"strings"
	"testing"
)

// TestV3LayoutCSPSafe verifies the v3 chrome carries the nonce'd script, links
// the same-origin stylesheet, and emits no CSP-violating inline styles or
// external asset hosts.
func TestV3LayoutCSPSafe(t *testing.T) {
	out := adminV3Layout("TESTNONCE", "Dashboard", "dashboard", &v3Settings{SiteName: "Demo"}, "<p>body</p>")
	assertCSPSafe(t, "adminV3Layout", out)
	if !strings.Contains(out, `<script nonce="TESTNONCE" src="/admin/v3/static/js/admin-v3.js"></script>`) {
		t.Error("v3 layout missing nonce'd script tag")
	}
	if !strings.Contains(out, `<link rel="stylesheet" href="/admin/v3/static/css/admin-v3.css">`) {
		t.Error("v3 layout missing same-origin stylesheet link")
	}
	if !strings.Contains(out, "Demo") {
		t.Error("v3 layout did not render site name")
	}
}

// TestV3LayoutEscapesTitle ensures a hostile page title cannot break out of the
// HTML context (defence against reflected XSS in the chrome).
func TestV3LayoutEscapesTitle(t *testing.T) {
	out := adminV3Layout("N", `</title><script>alert(1)</script>`, "dashboard", nil, "")
	if strings.Contains(out, "<script>alert(1)") {
		t.Error("v3 layout did not escape the page title")
	}
}

// TestV3LoginPageCSPSafe checks the standalone login page is CSP-clean and
// escapes the error message and prefilled email.
func TestV3LoginPageCSPSafe(t *testing.T) {
	out := v3LoginPage(`evil"<x>`, `<b>bad</b>`)
	assertCSPSafe(t, "v3LoginPage", out)
	if strings.Contains(out, "<b>bad</b>") {
		t.Error("login page did not escape error message")
	}
	if strings.Contains(out, `evil"<x>`) {
		t.Error("login page did not escape prefilled email")
	}
}

// TestV3SparklineEmpty returns empty string for no data and never panics.
func TestV3Sparkline(t *testing.T) {
	if v3Sparkline(nil) != "" {
		t.Error("expected empty string for nil series")
	}
	out := v3Sparkline([]int{0, 1, 3, 2, 5})
	if !strings.Contains(out, "<svg") || !strings.Contains(out, "sparkline__line") {
		t.Error("sparkline did not render expected SVG structure")
	}
	if strings.Contains(out, `style="`) {
		t.Error("sparkline emitted an inline style attribute (CSP violation)")
	}
	// Single point must not divide by zero.
	if got := v3Sparkline([]int{4}); !strings.Contains(got, "<svg") {
		t.Error("single-point sparkline did not render")
	}
}
