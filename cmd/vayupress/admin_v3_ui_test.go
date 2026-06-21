package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// TestV3LayoutCSPSafe verifies the v3 chrome carries the nonce'd script, links
// the same-origin stylesheet, and emits no CSP-violating inline styles or
// external asset hosts.
func TestV3LayoutCSPSafe(t *testing.T) {
	out := adminV3Layout("TESTNONCE", "Dashboard", "dashboard", &v3Settings{SiteName: "Demo"}, "<p>body</p>")
	assertCSPSafe(t, "adminV3Layout", out)
	if !strings.Contains(out, `<script nonce="TESTNONCE" src="/os/static/js/admin-v3.js"></script>`) {
		t.Error("v3 layout missing nonce'd script tag")
	}
	if !strings.Contains(out, `<link rel="stylesheet" href="/os/static/css/admin-v3.css">`) {
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

// TestV3EditorBodyCSPSafe verifies the block-editor shell is CSP-clean and
// escapes the slug, title, and embedded blocks JSON.
func TestV3EditorBodyCSPSafe(t *testing.T) {
	out := v3EditorBody(`slug"<x>`, `T"<i>`, `[{"type":"paragraph","text":"<script>x</script>"}]`)
	assertCSPSafe(t, "v3EditorBody", out)
	if strings.Contains(out, "<script>x</script>") {
		t.Error("editor body did not escape blocks JSON content")
	}
	if strings.Contains(out, `slug"<x>`) {
		t.Error("editor body did not escape slug")
	}
}

// TestListMediaItemsFiltersUnsafeNames ensures the media library only surfaces
// server-generated content-addressed names and silently ignores anything else
// (stray uploads, traversal-looking names, disallowed extensions).
func TestListMediaItemsFiltersUnsafeNames(t *testing.T) {
	dir := t.TempDir()
	prev := config.Cfg.MediaDir
	config.Cfg.MediaDir = dir
	t.Cleanup(func() { config.Cfg.MediaDir = prev })

	good := strings.Repeat("a", 32) + ".png"
	goodPDF := strings.Repeat("b", 32) + ".pdf"
	bad := []string{
		"evil.svg",
		"..%2fetc%2fpasswd",
		strings.Repeat("a", 32) + ".svg", // SVG never allowed
		"short.png",
		"notes.txt",
	}
	for _, n := range append([]string{good, goodPDF}, bad...) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	items := listMediaItems()
	if len(items) != 2 {
		t.Fatalf("want 2 safe items, got %d: %+v", len(items), items)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.Name] = true
		if !strings.HasPrefix(it.URL, "/media/") {
			t.Errorf("unexpected URL: %q", it.URL)
		}
	}
	if !got[good] || !got[goodPDF] {
		t.Errorf("expected safe names present, got %+v", got)
	}
}
