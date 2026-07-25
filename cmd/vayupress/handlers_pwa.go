package main

// handlers_pwa.go — PWA manifest, service worker, app icons, and chroma CSS.
//
// Installing the public site has to produce a REAL installed app, not a launcher
// shortcut. On Android, Chrome mints a WebAPK — an actual generated package that
// survives reboots like any other app — only when the site passes the full
// installability check: HTTPS, a manifest with a stable identity and icons of at
// least 192px, and a REGISTERED service worker. Miss any of it and the browser
// silently degrades "Install" to a legacy home-screen shortcut, which lives in the
// launcher's own database and is routinely wiped when the device restarts or the
// launcher rebuilds. That degradation is invisible at install time and only shows
// up later as "my app disappeared".

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
)

// Public web-app icons, generated from the brand mark by scripts/gen-pwa-icons.py.
// A real 192 and a real 512 are both required: the manifest used to declare one
// 256x256 file as being both sizes, and Android had to guess.

//go:embed assets/webapp-192.png
var webAppIcon192PNG []byte

//go:embed assets/webapp-512.png
var webAppIcon512PNG []byte

//go:embed assets/webapp-maskable-512.png
var webAppIconMaskablePNG []byte

//go:embed assets/webapp-apple-180.png
var webAppIconApplePNG []byte

// handleChromaCSS serves the chroma syntax-highlighting CSS, generated once
// at first request and cached in memory thereafter.
var (
	chromaCSSOnce  sync.Once
	chromaCSSBytes []byte
)

func (a *App) handleChromaCSS(w http.ResponseWriter, r *http.Request) {
	chromaCSSOnce.Do(func() {
		chromaCSSBytes = []byte(render.ChromaCSS())
	})
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(chromaCSSBytes) //nolint:errcheck
}

// handlePWAManifest returns the Web App Manifest for the public site. Name,
// description and theme colour follow site settings; identity and icons do not,
// because they decide whether the install is a real app.
func (a *App) handlePWAManifest(w http.ResponseWriter, r *http.Request) {
	s := render.GetActiveSettings()
	name := s.Name
	if name == "" {
		name = config.Cfg.Domain
	}
	if name == "" {
		// A manifest with no name is not installable at all, so never emit one.
		// Reaching here means neither a site name nor a domain is configured; a
		// generic name still gives a working, installable app.
		name = "VayuPress"
	}
	short := shortAppName(name, 12)
	manifest := map[string]interface{}{
		// id pins the app's identity independently of start_url. Without it, identity
		// IS start_url, so ever changing the landing page would make the browser treat
		// this as a DIFFERENT app and orphan the one already on the home screen. It
		// must never change once published.
		"id":               "/",
		"name":             name,
		"short_name":       short,
		"description":      s.Description,
		"start_url":        "/",
		"scope":            "/",
		"display":          "standalone",
		"display_override": []string{"standalone", "minimal-ui"},
		"orientation":      "any",
		"background_color": "#0a0f1a",
		"theme_color":      "#6366f1",
		"icons": []map[string]string{
			// Sizes here are the icons' true pixel dimensions. Declaring a size the
			// file does not have leaves the launcher to rescale whatever it gets.
			{"src": "/static/icons/webapp-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": "/static/icons/webapp-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
			// Maskable is a separate, padded, fully opaque asset. Android crops a
			// maskable icon to its own shape, so the previous entry — the unpadded
			// mark, declared maskable — was asking for its own edges to be clipped.
			{"src": "/static/icons/webapp-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
		},
		"categories": []string{"news", "blog"},
		"lang":       "en",
	}
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	// Re-fetched on every WebAPK update check, so keep it cacheable but short-lived:
	// a long-lived copy would delay a renamed site reaching already-installed apps.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	json.NewEncoder(w).Encode(manifest) //nolint:errcheck
}

// shortAppName trims a site name down to what fits under a launcher icon, which
// Android clips at roughly max characters. It counts runes rather than bytes — a
// byte slice would cut a multi-byte character in half and put a replacement glyph
// on the home screen — and prefers to break at a word boundary.
func shortAppName(name string, max int) string {
	r := []rune(name)
	if len(r) <= max {
		return name
	}
	for i := max; i > 0; i-- {
		if r[i-1] == ' ' {
			return strings.TrimRight(string(r[:i-1]), " ")
		}
	}
	return string(r[:max])
}

// handlePWARegisterJS serves the service-worker registration script. Same pattern
// as the other public scripts: a content-versioned, same-origin asset that
// satisfies script-src 'self' without a nonce.
func (a *App) handlePWARegisterJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(render.PWARegisterJS)) //nolint:errcheck
}

// handleServiceWorker serves the public service worker. Its presence is what makes
// the site installable as a real app; its caching is deliberately conservative.
func (a *App) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	// Service workers must be served from the root scope with the correct MIME type.
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Service-Worker-Allowed", "/")
	w.Write([]byte(serviceWorkerJS)) //nolint:errcheck
}

const serviceWorkerJS = `// VayuPress service worker — offline fallback for a real installed app.
//
// CACHE_NAME is bumped to v3. v2 stored page navigations stale-first and kept
// whatever a navigation returned, including pages the server marked private or
// no-store: on a shared device that could replay one member's account page to the
// next person, and it could re-show a cached sign-in form to somebody who was
// already signed in — defeating the server-side redirect before the origin was
// ever consulted. The activate handler deletes older caches, so upgrading a device
// self-heals.
const CACHE_NAME = 'vayupress-v3';

// Precached so a cold, offline launch still renders something. Fetched one by one:
// cache.addAll rejects atomically, so a single 404 in this list used to fail the
// whole install — and an install that fails leaves the app uninstallable.
const STATIC_ASSETS = [
  '/',
  '/static/chroma.css',
  '/static/icons/webapp-192.png',
  '/static/favicon-light.png',
  '/static/favicon-dark.png',
];

// Paths that must never be served from a cache: the console and the legacy admin
// path, plus every member surface (account, plans, sign-in, verification), because
// what they contain depends on who is asking.
function isPrivatePath(p) {
  return p === '/os' || p.indexOf('/os/') === 0 ||
         p === '/admin' || p.indexOf('/admin/') === 0 ||
         p.indexOf('/members') === 0 || p.indexOf('/signup') === 0 ||
         p.indexOf('/login') === 0 || p.indexOf('/pricing') === 0 ||
         p.indexOf('/api/') === 0;
}

// A response is only safe to store if the server did not mark it private, per-user
// or uncacheable. Honouring these headers is what keeps the worker from undoing the
// cache-correctness work done on the server.
function mayStore(response) {
  if (!response || !response.ok || response.type === 'opaque' || response.redirected) { return false; }
  const cc = response.headers.get('Cache-Control') || '';
  if (/no-store|private/i.test(cc)) { return false; }
  const vary = response.headers.get('Vary') || '';
  if (/cookie/i.test(vary) || vary === '*') { return false; }
  if (response.headers.get('Set-Cookie')) { return false; }
  return true;
}

self.addEventListener('install', event => {
  event.waitUntil(
    caches.open(CACHE_NAME).then(cache => Promise.all(
      STATIC_ASSETS.map(url => cache.add(new Request(url, { cache: 'reload' })).catch(() => null))
    )).then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', event => {
  event.waitUntil(
    caches.keys()
      .then(keys => Promise.all(keys.filter(k => k !== CACHE_NAME).map(k => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', event => {
  const req = event.request;
  if (req.method !== 'GET') { return; }
  const url = new URL(req.url);
  // Cross-origin requests are none of this worker's business.
  if (url.origin !== self.location.origin) { return; }
  if (isPrivatePath(url.pathname)) {
    return; // straight to the network, and never stored
  }

  // Cache-first for static assets. Their URLs carry content hashes, so a cached
  // hit is by definition the right bytes.
  if (url.pathname.indexOf('/static/') === 0 || url.pathname.indexOf('/media/') === 0) {
    event.respondWith(
      caches.match(req).then(hit => hit || fetch(req).then(res => {
        if (mayStore(res)) {
          const copy = res.clone();
          caches.open(CACHE_NAME).then(cache => cache.put(req, copy));
        }
        return res;
      }))
    );
    return;
  }

  // Network-FIRST for pages, with the cache only as an offline fallback. v2 was
  // stale-first, which showed yesterday's page to somebody who was online.
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req).then(res => {
        if (mayStore(res)) {
          const copy = res.clone();
          caches.open(CACHE_NAME).then(cache => cache.put(req, copy));
        }
        return res;
      }).catch(() => caches.match(req).then(hit => hit || caches.match('/').then(root => root || offlinePage())))
    );
    return;
  }
});

function offlinePage() {
  return new Response(
    '<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1">' +
    '<title>Offline</title>' +
    '<body style="margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#0a0f1a;color:#eef2f8;font:16px/1.5 system-ui,sans-serif">' +
    '<div style="text-align:center;padding:24px"><div style="font-size:44px">&#128244;</div>' +
    '<h1 style="font-size:20px;margin:.5em 0">You are offline</h1>' +
    '<p style="color:#b8c6dd;max-width:22rem">Reconnect to load this page.</p></div>',
    { headers: { 'Content-Type': 'text/html; charset=utf-8' }, status: 503 });
}
`
