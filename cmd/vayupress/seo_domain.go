package main

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// seo_domain.go — VayuDomains Stage 2c: per-domain sitemap / feed / robots.
//
// A single-domain install is untouched: handleSitemap/handleFeed/handleRobots
// serve the historic global artefacts (sitemap.xml, feed.xml, robots.txt) that
// CachePurge keeps fresh on every content change, byte-identical to before.
//
// When a secondary domain is registered (multiDomain), every host — primary and
// secondary alike — is served a per-domain artefact scoped by domain_id and
// carrying that host's URLs, so no domain's sitemap or feed ever advertises
// another domain's posts (which would 404 under the Stage 2b ownership gate).
// Per-domain artefacts are regenerated lazily on serve within a short freshness
// window rather than hooked into CachePurge, keeping the change self-contained.

// domSEOFreshWindow bounds how stale a lazily-regenerated per-domain artefact may
// be. Sitemaps and feeds are crawler-facing and inherently eventually-consistent,
// so a small window avoids a DB scan on every crawl while surfacing new posts
// promptly.
const domSEOFreshWindow = 90 * time.Second

// activeHost returns the resolved domain's host for the request, falling back to
// the configured primary domain when host resolution did not run.
func (a *App) activeHost(r *http.Request) string {
	if d, ok := activeDomain(r); ok && d.Host != "" {
		return d.Host
	}
	return config.Cfg.Domain
}

// domArtefactStale reports whether the cache file at rel is missing or older than
// the freshness window and therefore needs regenerating before it is served.
func domArtefactStale(rel string) bool {
	fi, err := os.Stat(filepath.Join(config.Cfg.CacheDir, rel))
	if err != nil {
		return true
	}
	return time.Since(fi.ModTime()) > domSEOFreshWindow
}

func (a *App) handleSitemap(w http.ResponseWriter, r *http.Request) {
	rel := "sitemap.xml"
	if a.multiDomain(r) {
		scope := a.contentScope(r)
		rel = "sitemap_d_" + domCacheDir(scope) + ".xml"
		if domArtefactStale(rel) {
			writeSitemapScoped(a.activeHost(r), scope, true, rel)
		}
	}
	http.ServeFile(w, r, filepath.Join(config.Cfg.CacheDir, rel))
}

func (a *App) handleFeed(w http.ResponseWriter, r *http.Request) {
	rel := "feed.xml"
	if a.multiDomain(r) {
		scope := a.contentScope(r)
		rel = "feed_d_" + domCacheDir(scope) + ".xml"
		if domArtefactStale(rel) {
			writeRSSScoped(a.activeHost(r), scope, true, rel)
		}
	}
	http.ServeFile(w, r, filepath.Join(config.Cfg.CacheDir, rel))
}

func (a *App) handleRobots(w http.ResponseWriter, r *http.Request) {
	rel := "robots.txt"
	if a.multiDomain(r) {
		rel = "robots_d_" + domCacheDir(a.contentScope(r)) + ".txt"
		if domArtefactStale(rel) {
			writeRobotsScoped(a.activeHost(r), rel)
		}
	}
	http.ServeFile(w, r, filepath.Join(config.Cfg.CacheDir, rel))
}
