package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/render"
)

// Everything under /static/css is served "public, immutable, max-age=31536000".
// That is only safe if the URL changes whenever the bytes do — an immutable
// response is never revalidated, so an unversioned URL pins a stylesheet in every
// browser and edge cache for a year. Two releases of member-portal restyling were
// invisible for exactly this reason: the widget requested a bare
// /static/css/portal.css, so the year-old copy kept winning.

// unversionedCSSLink matches an HTML stylesheet link under /static/css that
// carries no ?v= cache buster. It deliberately anchors on href=" so that route
// patterns and Go string constants naming the same file are not mistaken for
// references to it — the widget's own JS-side injection is covered by
// TestPortalStylesheetURLTracksItsContent instead.
var unversionedCSSLink = regexp.MustCompile(`href="(?:/os)?/static/css/[a-z0-9.-]+\.css"`)

// TestNoStylesheetIsServedFromAnUnversionedURL sweeps the Go sources that emit
// HTML and the portal widget for any /static/css reference without a version.
func TestNoStylesheetIsServedFromAnUnversionedURL(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("..", "..", "internal", "render")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			for _, m := range unversionedCSSLink.FindAllString(string(b), -1) {
				t.Errorf("%s: %q is served immutable for a year but carries no ?v= — "+
					"a restyle would be invisible until the cache expired", name, m)
			}
		}
	}
}

// TestPortalStylesheetURLTracksItsContent proves the mechanism works end to end:
// the served widget must point at a versioned stylesheet, and changing the
// stylesheet must move both that URL and the script's own version — otherwise a
// client holding a cached script would keep asking for the old stylesheet.
func TestPortalStylesheetURLTracksItsContent(t *testing.T) {
	dir := t.TempDir()
	cssDir := filepath.Join(dir, "css")
	if err := os.MkdirAll(cssDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(cssDir, "portal.css"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// This mutates process-wide asset state, so put it back for the other tests.
	original := ""
	if href := regexp.MustCompile(`/static/css/portal\.css\?v=([0-9a-f]+)`).FindStringSubmatch(render.PortalJSBody()); href != nil {
		original = href[1]
	}
	t.Cleanup(func() { render.SetPortalCSSVersion(original) })

	write(".vp-portal { color: red }")
	renderInitPortalCSS(t, dir)
	first, firstScript := portalCSSHrefFrom(t), render.PortalJSVersion()
	if !strings.Contains(first, "?v=") {
		t.Fatalf("widget requests %q with no version", first)
	}

	write(".vp-portal { color: blue }")
	renderInitPortalCSS(t, dir)
	second, secondScript := portalCSSHrefFrom(t), render.PortalJSVersion()

	if first == second {
		t.Error("the stylesheet URL must change when the stylesheet changes")
	}
	if firstScript == secondScript {
		t.Error("the script version must change too, or a cached script keeps requesting the old stylesheet")
	}
}

// renderInitPortalCSS re-runs the boot-time hashing against a staticDir.
func renderInitPortalCSS(t *testing.T, staticDir string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(staticDir, "css", "portal.css"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	render.SetPortalCSSVersion(hex.EncodeToString(sum[:8]))
}

// portalCSSHrefFrom extracts the stylesheet URL the served widget will inject.
func portalCSSHrefFrom(t *testing.T) string {
	t.Helper()
	body := render.PortalJSBody()
	m := regexp.MustCompile(`/static/css/portal\.css[^']*`).FindString(body)
	if m == "" {
		t.Fatal("the widget no longer injects portal.css")
	}
	return m
}
