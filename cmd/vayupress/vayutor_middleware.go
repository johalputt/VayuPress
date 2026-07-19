package main

// vayutor_middleware.go — request-path integration for VayuTor. A single
// middleware, mounted before the VayuDomains host resolver, does two things:
//
//   - Onion request (Host ends in ".onion"), CLEARNET Space: rewrite r.Host to
//     the clearnet host the onion maps to, so the SAME per-domain routing/content
//     serves over Tor with no duplication. The original onion host is
//     intentionally dropped (nothing distinguishes an onion visitor downstream —
//     privacy by design), except for a single aggregate visit tally.
//   - Onion request, TOR Space (VAYUOS_MODE=tor, ADR-0141): the onion IS the
//     site's own domain, so its Host is preserved and resolved as a first-class
//     VayuDomain — no clearnet rewrite. Only the count-only visit tally is kept.
//   - Clearnet request to a domain that has an onion: advertise it via the
//     Onion-Location response header so Tor Browser can offer/auto-switch to the
//     onion. This is a plain response header — no CSP change is required.

import (
	"context"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
)

// isOnionHost reports whether a request's Host header targets a .onion address.
// Used by the security-header middleware to harden onion responses (no HSTS —
// meaningless over http .onion — and a stricter Referrer-Policy).
func isOnionHost(hostHeader string) bool {
	return strings.HasSuffix(domain.NormalizeHost(hostHeader), ".onion")
}

// onionLocationEnabled reports whether clearnet responses should advertise their
// onion via the Onion-Location header (operator-controlled; default on).
func (a *App) onionLocationEnabled(ctx context.Context) bool {
	if a.siteSettings == nil {
		return true
	}
	return a.siteSettings.Get(ctx, settings.KeyTorOnionLocation) != "off"
}

// torOnionMiddleware maps onion Hosts to their clearnet domain and advertises
// onions on clearnet responses. A no-op when VayuTor is unavailable.
func (a *App) torOnionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.vayuTor == nil || !a.vayuTor.Available() {
			next.ServeHTTP(w, r)
			return
		}
		host := domain.NormalizeHost(r.Host)
		if strings.HasSuffix(host, ".onion") {
			if config.Cfg.OnionMode {
				// Onion-primary (ADR-0141): in a Tor Space the onion IS the site's
				// domain, so its Host must survive to downstream resolution — never
				// rewrite it to a clearnet host. Keep the aggregate visit tally.
				if isTorPageview(r) {
					a.vayuTor.IncVisit()
					// Path only — never the query string, which can carry tokens.
					a.vayuTor.IncPage(host, r.URL.Path)
				}
			} else if real, ok := a.vayuTor.HostForOnion(host); ok {
				// Clearnet Space: VayuTor overlays an onion on a clearnet domain, so
				// map the onion Host back to that domain's content.
				r.Host = real // serve the mapped domain's content over the onion
				if isTorPageview(r) {
					a.vayuTor.IncVisit()
					// Opt-in per-page tally (aggregate only; no-op unless enabled).
					// Path only — never the query string, which can carry tokens.
					a.vayuTor.IncPage(real, r.URL.Path)
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		// Clearnet: advertise the onion to Tor Browser (unless the operator turned
		// auto-advertising off in VayuTor hardening).
		if onion := a.vayuTor.OnionForHost(host); onion != "" && a.onionLocationEnabled(r.Context()) {
			w.Header().Set("Onion-Location", "http://"+onion+r.URL.RequestURI())
		}
		next.ServeHTTP(w, r)
	})
}

// isTorPageview reports whether a request counts as a page visit for the
// (count-only, no-PII) VayuTor tally: a GET for an actual page, not an asset,
// API, admin, or well-known path.
func isTorPageview(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	p := r.URL.Path
	for _, pre := range []string{"/os", "/api", "/static", "/assets", "/.well-known", "/favicon", "/robots", "/sitemap", "/feed", "/csp-report", "/health"} {
		if p == pre || strings.HasPrefix(p, pre+"/") || strings.HasPrefix(p, pre+".") {
			return false
		}
	}
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		switch strings.ToLower(p[i:]) {
		case ".css", ".js", ".mjs", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico",
			".webp", ".avif", ".woff", ".woff2", ".ttf", ".map", ".xml", ".json", ".txt", ".pdf":
			return false
		}
	}
	return true
}
