package main

// tor_world_proxy.go — "enter the Tor world" (ADR-0141).
//
// The Anonymous Tor Space is a SECOND, fully separate VayuPress instance with its
// own database, domains, mail and .onion (see internal/vayuos/torspace). Flipping
// the sidebar switch to Tor sets a per-browser "world view" cookie; while it is
// set, this middleware proxies the operator's admin console (/os/*) into that
// separate Tor instance, so they manage ITS data — its Tor domains, Tor blog, Tor
// mail, Tor chat — and never clearnet's. Flip back to Clearnet and the cookie is
// cleared, returning to the normal console. The two worlds keep separate
// databases; only the operator's authenticated admin view is bridged.
//
// The child authenticates the proxied requests by its OWN distinct API key (held
// by the parent), which the child checks before any session cookie — so no
// clearnet session or account crosses over. /os/world (the switch) and /os/logout
// are never proxied, so the operator can always flip back or sign out. Fail-safe:
// if the Tor world is not yet reachable, a clear "starting…" page with a Back to
// Clearnet link is shown instead of a broken console.

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"

	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// worldCookie names the per-browser view flag; value "tor" ⇒ view the Tor world.
const worldCookie = "vp_world"

// inTorWorldView reports whether this operator's browser is currently viewing the
// Tor world (set by the sidebar switch via /os/world?target=tor).
func inTorWorldView(r *http.Request) bool {
	c, err := r.Cookie(worldCookie)
	return err == nil && c.Value == "tor"
}

// torWorldMiddleware bridges the operator's console into the separate Tor-world
// instance while the Tor view is active. It is mounted INSIDE the authenticated
// /os group, so only a signed-in admin can ever reach the proxy.
func (a *App) torWorldMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The world switch and sign-out are ALWAYS parent-handled, so the operator
		// can flip back to clearnet or log out even while viewing the Tor world.
		if r.URL.Path == "/os/world" || r.URL.Path == "/os/logout" {
			next.ServeHTTP(w, r)
			return
		}
		// Not viewing Tor, no Tor world built, or not an admin → normal clearnet.
		if !inTorWorldView(r) || a.torSpace == nil || !a.isAdminRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		// Only the admin surface is bridged (never the public site).
		if len(r.URL.Path) < 3 || r.URL.Path[:3] != "/os" {
			next.ServeHTTP(w, r)
			return
		}
		if !a.torSpace.Running() {
			// The world was toggled on but its instance is still booting / not up.
			a.renderTorWorldUnavailable(w, r)
			return
		}
		a.proxyToTorWorld(w, r)
	})
}

// handleWorldSwitch flips the operator's view between the clearnet console and the
// Tor world. GET-only and side-effect-light: it just sets/clears the view cookie
// (enabling the Tor world itself is the separate CSRF-checked space toggle), so a
// plain link from either console can switch worlds. Admin-only.
func (a *App) handleWorldSwitch(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, "/os")
		return
	}
	if r.URL.Query().Get("target") == "tor" {
		// Make sure the Tor world exists / is running, then view it.
		if a.siteSettings != nil {
			_ = a.siteSettings.SetMany(r.Context(), map[string]string{settings.KeyTorSpaceEnabled: "on"})
		}
		if a.torSpace != nil {
			go a.reconcileTorSpace()
		}
		// SESSION cookie (no MaxAge): viewing the Tor world is a per-session context,
		// not a sticky preference. A 30-day cookie silently kept the operator in Tor
		// view for a month — so their clearnet /os console (including VayuTalk, which
		// then proxied to the SEPARATE Tor instance's relay) appeared broken while the
		// mobile app stayed on the clearnet relay. Session scope means a fresh browser
		// session always starts on Clearnet; entering Tor is a deliberate, visible act
		// each time (ADR-0141).
		http.SetCookie(w, &http.Cookie{
			Name: worldCookie, Value: "tor", Path: "/",
			SameSite: http.SameSiteLaxMode, HttpOnly: true, Secure: r.TLS != nil,
		})
	} else {
		// Back to clearnet: clear the view cookie (the Tor world keeps running).
		// Clear BOTH the current Path=/ cookie and any legacy Path=/os cookie from an
		// earlier build, so an operator can never get stuck viewing Tor. The deletion
		// cookies carry the SAME attributes the cookie was set with (line above) —
		// HttpOnly, SameSite and request-aware Secure (https clearnet gets Secure; the
		// http .onion world must not, or the delete would be dropped over http, and the
		// operator could never switch back). Mirroring them also satisfies the CodeQL
		// "cookie Secure attribute" scan without weakening the onion world.
		clearWorldCookie := func(path string) {
			http.SetCookie(w, &http.Cookie{
				Name: worldCookie, Value: "", Path: path,
				SameSite: http.SameSiteLaxMode, HttpOnly: true, Secure: r.TLS != nil, MaxAge: -1,
			})
		}
		clearWorldCookie("/")
		clearWorldCookie("/os")
	}
	http.Redirect(w, r, "/os", http.StatusSeeOther)
}

// proxyToTorWorld reverse-proxies one admin request into the Tor-world instance on
// its loopback port, authenticating with the child's own API key. It never leaks a
// clearnet session: the child checks the API key before any cookie.
func (a *App) proxyToTorWorld(w http.ResponseWriter, r *http.Request) {
	port := a.torSpace.Port()
	key := a.torSpace.APIKey()
	if port == 0 || key == "" {
		a.renderTorWorldUnavailable(w, r)
		return
	}
	// The parent's securityHeadersMiddleware already set CSP + security headers
	// (with the PARENT's nonce) on this response. The Tor-world instance sends its
	// OWN CSP (with its own nonce) for the page it returns; leaving the parent's in
	// place would collide and block the child's nonce-gated inline scripts. Drop the
	// parent's so the child's headers are authoritative for the proxied page.
	for _, h := range []string{
		"Content-Security-Policy", "Content-Security-Policy-Report-Only",
		"X-Frame-Options", "X-Content-Type-Options", "X-Xss-Protection",
		"Referrer-Policy", "Strict-Transport-Security",
	} {
		w.Header().Del(h)
	}
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		req.Host = target.Host
		// Authenticate as the Tor world's own admin (its distinct API key). Drop any
		// inbound Authorization so only our injected key is honoured.
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Del("X-Api-Key")
	}
	// Never surface a raw 502 to the operator — show the friendly "starting" page.
	proxy.ErrorHandler = func(rw http.ResponseWriter, rq *http.Request, _ error) {
		a.renderTorWorldUnavailable(rw, rq)
	}
	proxy.ServeHTTP(w, r)
}

// renderTorWorldUnavailable is the fail-safe shown when the Tor world is enabled
// but not yet reachable (still publishing its .onion / booting). It auto-refreshes
// and always offers a way back to the clearnet console.
func (a *App) renderTorWorldUnavailable(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8">
<meta http-equiv="refresh" content="4">
<title>Tor world starting…</title>
<link rel="stylesheet" href="/os/static/css/admin-os.css">
</head><body class="vp-os" data-space="tor">
<div style="max-width:34rem;margin:14vh auto;padding:0 1.5rem;text-align:center">
<h1>Starting your Tor world…</h1>
<p class="text-sm muted">Your anonymous world is booting and publishing its <code>.onion</code> — this can take a couple of minutes the first time. This page refreshes itself.</p>
<p><a class="btn btn--ghost" href="/os/world?target=clearnet">← Back to Clearnet</a></p>
</div>
<script nonce="` + nonce + `">setTimeout(function(){location.reload();},4000);</script>
</body></html>`))
}
