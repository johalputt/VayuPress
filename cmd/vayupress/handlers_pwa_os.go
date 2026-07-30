// SPDX-License-Identifier: Apache-2.0

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
// browser offers "Install app" and the installed app opens straight into /os/.
//
// START_URL MUST END IN A SLASH, and that is not cosmetic — it is the difference
// between a real installed app and an icon that vanishes on the next reboot.
//
// This manifest declared start_url "/os" against scope "/os/". Scope matching is
// a plain prefix test on the serialised URL, and "/os" does not begin with
// "/os/", so the start URL sat OUTSIDE its own scope. Two things followed. The
// service worker, registered with scope "/os/" (admin-os.js), could not control
// "/os" either — and Chrome's installability check requires a worker that
// controls the start URL. Failing it does not surface an error: the browser
// silently downgrades "Install" to a legacy home-screen shortcut, which lives in
// the launcher's database rather than as a package, and is routinely discarded
// when the device restarts. That is precisely the "my app disappeared after a
// reboot" report, and nothing in the install flow hints at which one you got.
//
// "id" deliberately stays "/os". Identity is independent of start_url and has no
// scope requirement, so keeping it preserves the app's identity for any install
// that did survive rather than orphaning it as a second app.
func (a *App) handleOSManifest(w http.ResponseWriter, r *http.Request) {
	manifest := map[string]any{
		"id":               "/os",
		"name":             "VayuOS",
		"short_name":       "VayuOS",
		"description":      "Your sovereign console — mail, chat, site, security and more, in one app.",
		"start_url":        "/os/",
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
// Service-Worker-Allowed), it is deliberately a ZERO-CACHE worker: VayuOS must be
// live on every device, so the worker never stores or replays any console
// response. It exists only to make the console installable and to show a tiny
// offline notice for a failed navigation. Because it caches nothing, an update
// (or a themed change) is visible the instant the server serves it — no stale
// panel can ever be shown from a worker cache. It also purges any cache a previous
// worker version created, so upgrading to this worker self-heals a stale device.
func (a *App) handleOSServiceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Service-Worker-Allowed", "/os/")
	_, _ = w.Write([]byte(osServiceWorkerJS))
}

const osServiceWorkerJS = `// VayuOS console service worker — ZERO-CACHE, always live.
// It never caches a console response, so no device can ever show a stale VayuOS.

self.addEventListener('install', function () { self.skipWaiting(); });

self.addEventListener('activate', function (e) {
  // Purge EVERY cache (including any this app made in an earlier version): VayuOS
  // is never served from a cache, so this self-heals a device that was showing an
  // old build, then takes control of open pages immediately.
  e.waitUntil(
    caches.keys()
      .then(function (keys) { return Promise.all(keys.map(function (k) { return caches.delete(k); })); })
      .then(function () { return self.clients.claim(); })
  );
});

self.addEventListener('notificationclick', function (e) {
  // New-mail notifications (posted via registration.showNotification from
  // admin-os.js) carry the target mailbox URL in data.url. Focus an existing
  // console window and steer it there, or open a fresh one — so a single tap
  // lands on the mailbox, even from an installed PWA on mobile.
  e.notification.close();
  var url = (e.notification.data && e.notification.data.url) || '/os';
  e.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(function (list) {
      for (var i = 0; i < list.length; i++) {
        var c = list[i];
        if ('focus' in c) {
          c.focus();
          if (c.navigate) { try { c.navigate(url); } catch (err) { /* cross-origin guard */ } }
          return;
        }
      }
      if (self.clients.openWindow) return self.clients.openWindow(url);
    })
  );
});

self.addEventListener('fetch', function (e) {
  var req = e.request;
  if (req.method !== 'GET' || req.mode !== 'navigate') return;
  // Do NOT proxy a navigation the browser can perform itself.
  //
  // This handler used to fetch every navigation on the page's behalf. Two
  // variants were tried and both broke the console, for the same underlying
  // reason: a worker is a bad place to stand between a browser and a redirect.
  //
  //   fetch(req)                        -> a followed redirect comes back with
  //                                        .redirected = true, and the browser
  //                                        REFUSES that response for a
  //                                        navigation. The world toggle died.
  //   fetch with redirect set to manual -> comes back as an opaqueredirect:
  //                                        status 0, Location not readable. The
  //                                        browser has to resolve it, which
  //                                        re-enters this handler, which proxies
  //                                        again. The console's whole sign-in
  //                                        path is redirects (/os/ -> /os/login
  //                                        -> /os), so an installed app walked
  //                                        that ladder until it hit the redirect
  //                                        ceiling: ERR_TOO_MANY_REDIRECTS on
  //                                        every launch, clearing site data the
  //                                        only escape. A browser tab was fine,
  //                                        because a tab has no worker.
  //
  // Native navigation handles redirects, cookies and auth correctly and needs no
  // help. So the online path returns without calling respondWith at all, and the
  // worker keeps doing the jobs only it can do (skipWaiting, the cache purge on
  // activate, notification clicks). Still zero-cache, still always live.
  //
  // The one thing worth intercepting is a device with no connection, where the
  // branded notice beats a generic error page. navigator.onLine is only
  // trustworthy in the negative — false means no link at all — so that is the
  // only case handled here. Online-but-unreachable (server down, captive portal)
  // now falls through to the browser's own offline page, which is a fair trade
  // for a console that can actually be signed into.
  if (navigator.onLine !== false) return;
  e.respondWith((function () {
      return new Response(
        '<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1">' +
        '<title>VayuOS — offline</title>' +
        '<body style="margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#080e1a;color:#eef2f8;font:16px/1.5 system-ui,sans-serif">' +
        '<div style="text-align:center;padding:24px"><div style="font-size:44px">📴</div>' +
        '<h1 style="font-size:20px;margin:.5em 0">You are offline</h1>' +
        '<p style="color:#b8c6dd;max-width:22rem">VayuOS needs a connection to load your console. Reconnect and try again.</p></div>',
        { headers: { 'Content-Type': 'text/html; charset=utf-8' }, status: 503 });
  })());
});
`
