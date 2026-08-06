// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	htmpl "html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// TestOSLayoutCSPSafe verifies the os chrome carries the nonce'd script, links
// the same-origin stylesheet, and emits no CSP-violating inline styles or
// external asset hosts.
func TestOSLayoutCSPSafe(t *testing.T) {
	out := adminOSLayout("TESTNONCE", "Dashboard", "dashboard", &osSettings{SiteName: "Demo"}, htmpl.HTML("<p>body</p>"))
	assertCSPSafe(t, "adminOSLayout", out)
	if !strings.Contains(out, `<script nonce="TESTNONCE" src="/os/static/js/admin-os.js?v=`) {
		t.Error("os layout missing nonce'd, cache-busted script tag")
	}
	if !strings.Contains(out, `<link rel="stylesheet" href="/os/static/css/admin-os.css?v=`) {
		t.Error("os layout missing same-origin stylesheet link")
	}
	if !strings.Contains(out, "Demo") {
		t.Error("os layout did not render site name")
	}
	// Self-hosted HTMX must be wired same-origin (script-src 'self'), deferred,
	// and configured to skip its injected indicator <style> so style-src 'self'
	// is never violated.
	if !strings.Contains(out, `<script src="/static/js/htmx.min.js?v=`) || !strings.Contains(out, `" defer></script>`) {
		t.Error("os layout missing deferred same-origin HTMX script tag")
	}
	// Indicator styles must stay OFF (no injected <style>, keeps style-src 'self');
	// globalViewTransitions is ON so HTMX swaps ride the native View Transitions
	// API (ADR-0136). Assert both flags independently so either can move without a
	// brittle exact-JSON match.
	if !strings.Contains(out, `name="htmx-config"`) ||
		!strings.Contains(out, `"includeIndicatorStyles":false`) {
		t.Error("os layout missing htmx-config meta with indicator styles off (CSP)")
	}
	if !strings.Contains(out, `"globalViewTransitions":true`) {
		t.Error("os layout must enable HTMX globalViewTransitions (ADR-0136)")
	}
	// HTMX writes must never fail silently: the layout wires the CSRF header shim
	// and an error handler that surfaces failures via the toast API.
	for _, want := range []string{"htmx:configRequest", "htmx:responseError", "htmx:sendError", "window.vpToast"} {
		if !strings.Contains(out, want) {
			t.Errorf("os layout missing HTMX glue %q (CSRF + failure feedback)", want)
		}
	}
	// Accessibility: an aria-live region + an afterRequest handler announce HTMX
	// outcomes to assistive tech (WCAG 2.2 AA).
	if !strings.Contains(out, `id="vp-live"`) || !strings.Contains(out, `aria-live="polite"`) {
		t.Error("os layout missing the vp-live aria-live announce region")
	}
	if !strings.Contains(out, "htmx:afterRequest") {
		t.Error("os layout missing the htmx:afterRequest announce handler")
	}
}

// TestOSTopbarSpaceBadge verifies the ADR-0141 Space-mode indicator in the admin
// topbar: a clearnet install shows a "Clearnet" badge, a Tor install shows a
// "Tor" badge, exactly one is ever present, and neither breaks the strict admin
// CSP (no inline style / external host).
func TestOSTopbarSpaceBadge(t *testing.T) {
	prev := config.Cfg.OnionMode
	defer func() { config.Cfg.OnionMode = prev }()

	t.Run("clearnet space", func(t *testing.T) {
		config.Cfg.OnionMode = false
		out := adminOSLayout("N", "Dashboard", "dashboard", &osSettings{SiteName: "Demo"}, htmpl.HTML("<p>x</p>"))
		assertCSPSafe(t, "spaceBadge/clearnet", out)
		if !strings.Contains(out, `class="space-badge space-badge--clearnet"`) {
			t.Error("clearnet install must render the clearnet Space badge")
		}
		if strings.Contains(out, "space-badge--tor") {
			t.Error("clearnet install must not render the Tor Space badge")
		}
	})

	t.Run("tor space", func(t *testing.T) {
		config.Cfg.OnionMode = true
		out := adminOSLayout("N", "Dashboard", "dashboard", &osSettings{SiteName: "Demo"}, htmpl.HTML("<p>x</p>"))
		assertCSPSafe(t, "spaceBadge/tor", out)
		if !strings.Contains(out, `class="space-badge space-badge--tor"`) {
			t.Error("Tor install must render the Tor Space badge")
		}
		if strings.Contains(out, "space-badge--clearnet") {
			t.Error("Tor install must not render the clearnet Space badge")
		}
	})
}

// TestHTMXAssetServed verifies the self-hosted HTMX library is compiled into the
// binary (via StaticFS) and served at /static/js/htmx.min.js with a JavaScript
// content type — same-origin, no CDN, satisfying script-src 'self'.
func TestHTMXAssetServed(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest(http.MethodGet, "/static/js/htmx.min.js", nil)
	rec := httptest.NewRecorder()
	a.handleHTMXJS(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleHTMXJS status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/javascript; charset=utf-8" {
		t.Errorf("handleHTMXJS content-type = %q, want application/javascript", ct)
	}
	if !strings.Contains(rec.Body.String(), "htmx") {
		t.Error("handleHTMXJS did not serve the HTMX library body")
	}
	if rec.Body.Len() < 10000 {
		t.Errorf("handleHTMXJS body too small (%d bytes) — asset likely not embedded", rec.Body.Len())
	}
}

// TestOSLayoutEscapesTitle ensures a hostile page title cannot break out of the
// HTML context (defence against reflected XSS in the chrome).
func TestOSLayoutEscapesTitle(t *testing.T) {
	out := adminOSLayout("N", `</title><script>alert(1)</script>`, "dashboard", nil, htmpl.HTML(""))
	if strings.Contains(out, "<script>alert(1)") {
		t.Error("os layout did not escape the page title")
	}
}

// TestOSLoginPageCSPSafe checks the standalone login page is CSP-clean and
// escapes the error message and prefilled email.
func TestOSLoginPageCSPSafe(t *testing.T) {
	out := osLoginPage(`evil"<x>`, `<b>bad</b>`, "")
	assertCSPSafe(t, "osLoginPage", out)
	if strings.Contains(out, "<b>bad</b>") {
		t.Error("login page did not escape error message")
	}
	if strings.Contains(out, `evil"<x>`) {
		t.Error("login page did not escape prefilled email")
	}
}

// TestOSSparklineEmpty returns empty string for no data and never panics.
func TestOSSparkline(t *testing.T) {
	if osSparkline(nil) != "" {
		t.Error("expected empty string for nil series")
	}
	out := osSparkline([]int{0, 1, 3, 2, 5})
	if !strings.Contains(out, "<svg") || !strings.Contains(out, "sparkline__line") {
		t.Error("sparkline did not render expected SVG structure")
	}
	if strings.Contains(out, `style="`) {
		t.Error("sparkline emitted an inline style attribute (CSP violation)")
	}
	// Single point must not divide by zero.
	if got := osSparkline([]int{4}); !strings.Contains(got, "<svg") {
		t.Error("single-point sparkline did not render")
	}
}

// TestOSEditorBodyCSPSafe verifies the block-editor shell is CSP-clean and
// escapes the slug, title, and embedded blocks JSON.
func TestOSEditorBodyCSPSafe(t *testing.T) {
	out := osEditorBody(`slug"<x>`, `T"<i>`, `[{"type":"paragraph","text":"<script>x</script>"}]`, "")
	assertCSPSafe(t, "osEditorBody", out)
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
	// A content-addressed .svg is a name this server writes: the upload path
	// accepts SVG (sanitised on the way in) and the media quota charges for the
	// result, so the library has to show it. Hiding it was how a full library
	// became a state the panel could not get out of — the operator could see
	// neither the files holding the ceiling down nor a control to remove them.
	// "evil.svg" stays in the list below, which is the part that matters: the
	// extension is not what admits a file, the content-addressed name is.
	goodSVG := strings.Repeat("c", 32) + ".svg"
	bad := []string{
		"evil.svg",
		"..%2fetc%2fpasswd",
		"short.png",
		"notes.txt",
	}
	for _, n := range append([]string{good, goodPDF, goodSVG}, bad...) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	items := listMediaItems()
	if len(items) != 3 {
		t.Fatalf("want 3 safe items, got %d: %+v", len(items), items)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.Name] = true
		if !strings.HasPrefix(it.URL, "/media/") {
			t.Errorf("unexpected URL: %q", it.URL)
		}
	}
	if !got[good] || !got[goodPDF] || !got[goodSVG] {
		t.Errorf("expected safe names present, got %+v", got)
	}
}

// The delete endpoint removes files this server wrote, and nothing else.
//
// This exists because a mutation proved nothing was holding that line: replacing
// the name check with a test for a non-empty string left the entire suite green.
// The handler is os.Remove(filepath.Join(MediaDir, name)) over a caller-supplied
// name, so the allowlist is the only thing between an author-level session and
// deleting any path the process can reach — the database beside MediaDir
// included. An untested control on that shape of code is the one worth writing
// down.
func TestMediaDeleteRemovesOnlyWhatThisServerWrote(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "media")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	prev := config.Cfg.MediaDir
	config.Cfg.MediaDir = dir
	t.Cleanup(func() { config.Cfg.MediaDir = prev })

	outside := filepath.Join(root, "vayupress.db")
	if err := os.WriteFile(outside, []byte("database"), 0o644); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(stray, []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string][]string{"names": {
		"../vayupress.db",
		"..%2fvayupress.db",
		"notes.txt",
		"evil.svg",
		strings.Repeat("a", 31) + ".png", // one hex char short
	}})
	if err != nil {
		t.Fatalf("marshal delete body: %v", err)
	}
	rec := httptest.NewRecorder()
	(&App{}).handleOSMediaDelete(rec, httptest.NewRequest(http.MethodPost, "/os/media/delete", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete returned %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad delete response: %v (%s)", err, rec.Body.String())
	}
	if out.Deleted != 0 {
		t.Errorf("the endpoint reported %d deletion(s) for a request naming nothing this server wrote",
			out.Deleted)
	}
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Errorf("a file OUTSIDE the media directory was removed through the media endpoint: %v.\n"+
			"The name is joined onto MediaDir and passed to os.Remove, so anything that gets past "+
			"the allowlist is an authenticated delete of any path this process can reach.", statErr)
	}
	if _, statErr := os.Stat(stray); statErr != nil {
		t.Errorf("a file the library does not manage was removed: %v", statErr)
	}
}
