package main

// handlers_pwa_os.go — installable-app (PWA) support for the VayuOS console.
//
// The public site already has its own manifest + service worker (handlers_pwa.go),
// whose worker deliberately NEVER caches /os (authenticated, per-user pages). This
// file adds a SEPARATE, installable app for the CONSOLE itself: its own manifest
// (start_url /os, scope /os/), its own worker scoped to /os/ that is privacy-first
// (network-only for pages — never a cached dashboard — and cache-first only for the
// versioned static shell), plus the app icons. Installing it puts VayuOS on the
// home screen / desktop as a fast, standalone app on both mobile and desktop.

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

//go:embed assets/vayuos-192.png
var osIcon192PNG []byte

//go:embed assets/vayuos-512.png
var osIcon512PNG []byte

//go:embed assets/vayuos-maskable-512.png
var osIconMaskablePNG []byte

//go:embed assets/vayuos-apple-180.png
var osIconApplePNG []byte

// handleOSManifest returns the Web App Manifest for the VayuOS console, so the
// browser offers "Install app" and the installed app opens straight into /os.
func (a *App) handleOSManifest(w http.ResponseWriter, r *http.Request) {
	manifest := map[string]any{
		"id":               "/os",
		"name":             "VayuOS",
		"short_name":       "VayuOS",
		"description":      "Your sovereign console — mail, chat, site, security and more, in one app.",
		"start_url":        "/os",
		"scope":            "/os/",
		"display":          "standalone",
		"display_override": []string{"standalone", "minimal-ui"},
		"orientation":      "any",
		"background_color": "#080e1a",
		"theme_color":      "#080e1a",
		"lang":             "en",
		"categories":       []string{"productivity", "business", "utilities"},
		"icons": []map[string]string{
			{"src": "/os/static/icons/vayuos-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": "/os/static/icons/vayuos-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
			{"src": "/os/static/icons/vayuos-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(manifest)
}

// handleOSServiceWorker serves the console's service worker. Scoped to /os/ (see
// Service-Worker-Allowed), it is intentionally minimal and privacy-first: pages
// are always fetched from the network (never a cached, per-operator dashboard),
// and only the versioned, non-sensitive static shell (CSS/JS/icons under
// /os/static/) is cached — which is what makes the installed app feel instant.
func (a *App) handleOSServiceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Service-Worker-Allowed", "/os/")
	_, _ = w.Write([]byte(osServiceWorkerJS))
}

const osServiceWorkerJS = `// VayuOS console service worker — privacy-first, static-shell only.
const CACHE = 'vayuos-shell-v1';

self.addEventListener('install', function (e) { self.skipWaiting(); });

self.addEventListener('activate', function (e) {
  e.waitUntil(caches.keys().then(function (keys) {
    return Promise.all(keys.map(function (k) { if (k !== CACHE) return caches.delete(k); }));
  }).then(function () { return self.clients.claim(); }));
});

self.addEventListener('fetch', function (e) {
  var req = e.request;
  if (req.method !== 'GET') return;
  var url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  // Versioned static shell (?v=<hash>): cache-first — this is what makes the
  // installed app open instantly. Never anything authenticated or per-user.
  if (url.pathname.indexOf('/os/static/') === 0) {
    e.respondWith(
      caches.match(req).then(function (hit) {
        if (hit) return hit;
        return fetch(req).then(function (res) {
          if (res && res.ok) { var cp = res.clone(); caches.open(CACHE).then(function (c) { c.put(req, cp); }); }
          return res;
        });
      })
    );
    return;
  }

  // Everything else (the console's pages and APIs): always the network, so an
  // operator never sees a stale or another user's dashboard. When offline, a
  // navigation falls back to a tiny built-in offline notice.
  if (req.mode === 'navigate') {
    e.respondWith(fetch(req).catch(function () {
      return new Response(
        '<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1">' +
        '<title>VayuOS — offline</title>' +
        '<body style="margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#080e1a;color:#eef2f8;font:16px/1.5 system-ui,sans-serif">' +
        '<div style="text-align:center;padding:24px"><div style="font-size:44px">📴</div>' +
        '<h1 style="font-size:20px;margin:.5em 0">You are offline</h1>' +
        '<p style="color:#9db0cc;max-width:22rem">VayuOS needs a connection to load your console. Reconnect and try again.</p></div>',
        { headers: { 'Content-Type': 'text/html; charset=utf-8' }, status: 503 });
    }));
    return;
  }
});
`
