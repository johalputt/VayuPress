// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestOSManifest(t *testing.T) {
	rr := httptest.NewRecorder()
	(&App{}).handleOSManifest(rr, httptest.NewRequest(http.MethodGet, "/os/manifest.webmanifest", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "manifest") {
		t.Errorf("Content-Type = %q, want a manifest type", ct)
	}
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	// THE invariant, not a pair of literals: the start URL must sit inside the
	// scope. Scope matching is a plain prefix test on the serialised URL, so a
	// start_url of "/os" against scope "/os/" is OUTSIDE its own scope — and the
	// worker registered at scope "/os/" cannot control it either. Chrome's
	// installability check needs a worker controlling the start URL; failing it
	// silently downgrades the install to a home-screen shortcut, which a device
	// restart discards. The previous version of this test asserted exactly that
	// broken pair, so it locked the bug in instead of catching it.
	start, _ := m["start_url"].(string)
	scope, _ := m["scope"].(string)
	if scope == "" || !strings.HasSuffix(scope, "/") {
		t.Errorf("scope = %q, must be non-empty and end in a slash", scope)
	}
	if !strings.HasPrefix(start, scope) {
		t.Errorf("start_url %q is outside scope %q — the install degrades to a shortcut that a reboot deletes", start, scope)
	}
	// Identity must stay pinned. Changing it orphans every existing install as a
	// separate app, and it is independent of start_url so it never needs to move.
	if m["id"] != "/os" {
		t.Errorf("id = %v, want /os (changing it orphans installed apps)", m["id"])
	}
	icons, ok := m["icons"].([]any)
	if !ok || len(icons) == 0 {
		t.Fatal("manifest must declare icons")
	}
	// Android needs a real 192 and a real 512 to mint a package, plus a maskable
	// for its own icon shape.
	var have192, have512, haveMaskable bool
	for _, ic := range icons {
		m, _ := ic.(map[string]any)
		switch m["sizes"] {
		case "192x192":
			have192 = true
		case "512x512":
			have512 = true
		}
		if m["purpose"] == "maskable" {
			haveMaskable = true
		}
	}
	if !have192 || !have512 || !haveMaskable {
		t.Errorf("icons must include 192x192, 512x512 and a maskable entry (got 192=%v 512=%v maskable=%v)",
			have192, have512, haveMaskable)
	}
}

// TestOSStartURLIsRoutable pins the other half of the fix. A start_url inside
// scope is worthless if nothing serves it: the installability check follows the
// URL, and a 404 — or a redirect — reads as an uninstallable app.
func TestOSStartURLIsRoutable(t *testing.T) {
	src, err := os.ReadFile("admin_os_ui.go")
	if err != nil {
		t.Fatalf("read admin_os_ui.go: %v", err)
	}
	if !strings.Contains(string(src), `pr.Get("/os/", a.handleOSDashboard)`) {
		t.Error(`"/os/" must be routed to the dashboard: it is the installed app's start_url, ` +
			`and it must answer 200 rather than 404 or a redirect`)
	}
}

func TestOSServiceWorker(t *testing.T) {
	rr := httptest.NewRecorder()
	(&App{}).handleOSServiceWorker(rr, httptest.NewRequest(http.MethodGet, "/os/sw.js", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := rr.Header().Get("Service-Worker-Allowed"); got != "/os/" {
		t.Errorf("Service-Worker-Allowed = %q, want /os/", got)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want javascript", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "addEventListener") {
		t.Error("service worker body looks empty")
	}
	// New-mail notifications are shown via the worker so the installed PWA and
	// mobile browsers work; the click must route to the mailbox URL.
	if !strings.Contains(body, "notificationclick") {
		t.Error("service worker must handle notificationclick to open the mailbox on tap")
	}
	// It must remain a zero-cache worker — never persist a console response.
	if !strings.Contains(body, "caches.keys()") || strings.Contains(body, "caches.open(") {
		t.Error("service worker must stay zero-cache (purge caches, never open one)")
	}
	// Navigations must be fetched with redirect:'manual' so a server redirect (the
	// Clearnet↔Tor world switch, logout, login) is not turned into a `redirected`
	// response the browser refuses to use for a navigation.
	if !strings.Contains(body, "redirect: 'manual'") {
		t.Error("navigation fetch must use redirect:'manual' or server redirects break in the installed PWA")
	}
}
