package main

// handlers_pwa.go — PWA manifest, service worker, and chroma CSS handler.

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
)

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

// handlePWAManifest returns a Web App Manifest so the site can be installed
// as a PWA. Theme colours, name, and icons are derived from site settings.
func (a *App) handlePWAManifest(w http.ResponseWriter, r *http.Request) {
	s := render.GetActiveSettings()
	name := s.Name
	if name == "" {
		name = config.Cfg.Domain
	}
	manifest := map[string]interface{}{
		"name":             name,
		"short_name":       name,
		"description":      s.Description,
		"start_url":        "/",
		"display":          "standalone",
		"background_color": "#0a0f1a",
		"theme_color":      "#6366f1",
		"icons": []map[string]string{
			{"src": "/static/favicon-light.png", "sizes": "192x192", "type": "image/png"},
			{"src": "/static/favicon-light.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
		},
		"categories": []string{"news", "blog"},
		"lang":       "en",
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	json.NewEncoder(w).Encode(manifest) //nolint:errcheck
}

// handleServiceWorker serves a minimal service worker that caches static assets
// and serves them offline. It uses a stale-while-revalidate strategy for pages
// and a cache-first strategy for CSS/JS/images.
func (a *App) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	// Service workers must be served from the root scope with correct MIME type.
	// Using a fixed cache name lets us version-bust on deploy by changing CACHE_NAME.
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Service-Worker-Allowed", "/")
	w.Write([]byte(serviceWorkerJS)) //nolint:errcheck
}

const serviceWorkerJS = `// VayuPress service worker — offline-first for static assets.
const CACHE_NAME = 'vayupress-v1';
const STATIC_ASSETS = [
  '/',
  '/static/chroma.css',
  '/static/favicon-light.png',
  '/static/favicon-dark.png',
  '/feed.xml',
];

self.addEventListener('install', event => {
  event.waitUntil(
    caches.open(CACHE_NAME).then(cache => cache.addAll(STATIC_ASSETS))
  );
  self.skipWaiting();
});

self.addEventListener('activate', event => {
  event.waitUntil(
    caches.keys().then(keys =>
      Promise.all(keys.filter(k => k !== CACHE_NAME).map(k => caches.delete(k)))
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', event => {
  const url = new URL(event.request.url);
  // Cache-first for static assets (CSS, JS, images, fonts).
  if (url.pathname.startsWith('/static/') || url.pathname.startsWith('/media/')) {
    event.respondWith(
      caches.match(event.request).then(cached => {
        if (cached) return cached;
        return fetch(event.request).then(response => {
          if (response.ok) {
            const clone = response.clone();
            caches.open(CACHE_NAME).then(cache => cache.put(event.request, clone));
          }
          return response;
        });
      })
    );
    return;
  }
  // Never cache authenticated admin pages — they must always hit the network
  // (avoids serving a stale/foreign admin view from a shared-device cache).
  if (url.pathname === '/admin' || url.pathname.startsWith('/admin/')) {
    event.respondWith(fetch(event.request));
    return;
  }
  // Stale-while-revalidate for HTML pages.
  if (event.request.mode === 'navigate') {
    event.respondWith(
      caches.open(CACHE_NAME).then(cache =>
        cache.match(event.request).then(cached => {
          const network = fetch(event.request).then(response => {
            if (response.ok) cache.put(event.request, response.clone());
            return response;
          });
          return cached || network;
        })
      )
    );
    return;
  }
  event.respondWith(fetch(event.request));
});
`
