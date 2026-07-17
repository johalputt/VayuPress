package main

// vayutor_middleware.go — request-path integration for VayuTor. A single
// middleware, mounted before the VayuDomains host resolver, does two things:
//
//   - Onion request (Host ends in ".onion"): rewrite r.Host to the clearnet
//     host the onion maps to, so the SAME per-domain routing/content serves over
//     Tor with no duplication. The original onion host is intentionally dropped
//     (nothing distinguishes an onion visitor downstream — privacy by design),
//     except for a single aggregate visit tally.
//   - Clearnet request to a domain that has an onion: advertise it via the
//     Onion-Location response header so Tor Browser can offer/auto-switch to the
//     onion. This is a plain response header — no CSP change is required.

import (
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/domain"
)

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
			if real, ok := a.vayuTor.HostForOnion(host); ok {
				r.Host = real // serve the mapped domain's content over the onion
				if isTorPageview(r) {
					a.vayuTor.IncVisit()
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		// Clearnet: advertise the onion to Tor Browser.
		if onion := a.vayuTor.OnionForHost(host); onion != "" {
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
