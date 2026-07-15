package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestServiceWorkerNeverCachesConsole guards the fix for the bug where the PWA
// service worker served a stale /os/login from Cache Storage — surviving hard
// refresh and browser restarts — which silently defeated the server-side
// "already signed in → redirect to /os" logic (and risked serving one operator's
// dashboard to another on a shared device).
//
// The service worker MUST route the whole console (/os and /os/*) and the legacy
// /admin path straight to the network, never through the cache, and that guard
// MUST appear before the navigate/stale-while-revalidate branch.
func TestServiceWorkerNeverCachesConsole(t *testing.T) {
	sw := serviceWorkerJS

	// The network-only guard must name the console paths.
	for _, want := range []string{
		"url.pathname === '/os'",
		"url.pathname.startsWith('/os/')",
		"url.pathname === '/admin'",
		"url.pathname.startsWith('/admin/')",
	} {
		if !strings.Contains(sw, want) {
			t.Errorf("service worker missing console network-only guard %q", want)
		}
	}

	// Ordering: the console guard must run BEFORE the stale-while-revalidate
	// navigate branch, otherwise a navigation to /os/login would be served from
	// cache before the guard is reached.
	guardIdx := strings.Index(sw, "url.pathname.startsWith('/os/')")
	navIdx := strings.Index(sw, "event.request.mode === 'navigate'")
	if guardIdx < 0 || navIdx < 0 {
		t.Fatal("service worker is missing the console guard or the navigate branch")
	}
	if guardIdx > navIdx {
		t.Error("console network-only guard must precede the navigate/stale-while-revalidate branch")
	}

	// The cache name must be bumped past v1 so the activate handler purges any
	// v1 cache that wrongly stored a console page.
	if !strings.Contains(sw, "CACHE_NAME = 'vayupress-v2'") {
		t.Error("CACHE_NAME must be bumped to v2 to purge the poisoned v1 cache")
	}

	// /os must not be part of the precached static asset list.
	staticList := sw[strings.Index(sw, "STATIC_ASSETS"):strings.Index(sw, "self.addEventListener('install'")]
	if strings.Contains(staticList, "/os") {
		t.Error("STATIC_ASSETS must not precache any /os console page")
	}
}

// TestServiceWorkerServedNoStore verifies the SW script itself is served
// uncacheable, so browsers pick up a new version promptly (this is what lets the
// fixed worker replace the buggy one without manual intervention).
func TestServiceWorkerServedNoStore(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	rec := httptest.NewRecorder()
	a.handleServiceWorker(rec, req)

	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") && !strings.Contains(cc, "no-store") {
		t.Errorf("/sw.js Cache-Control = %q, want no-cache/no-store so the worker updates", cc)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("/sw.js Content-Type = %q, want a javascript type", ct)
	}
}
