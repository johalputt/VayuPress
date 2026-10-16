// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/url"
	"strings"
)

// refuseCrossOrigin refuses a write that a browser sent from another origin.
//
// A reader's cookie is SameSite=Lax, and the routes behind this take no CSRF
// token: Lax keeps another SITE's page from using the cookie, and that was
// taken to be enough. It is not enough for another ORIGIN on the same site,
// which Lax treats as the site itself: a hosted domain such as
// shop.example.com, whose owner uploads its own scripts, could rename the
// readers of example.com, sign them out, claim them a mailbox or post as
// them (the 3.18.3 audit, member_origin_test.go).
//
// A browser names the origin of every write it sends across origins, so the
// Origin header is the check: another host is refused, and "null" (a
// sandboxed frame, a redirected form) with it. A request without one is not
// a browser's cross-origin write (an app, a server, an old client) and goes
// on, as it did. The host is compared without the scheme: behind a proxy
// that ends TLS, the request arrives as http for a page served over https.
//
// The Tor-world proxy uses it too (proxyToTorWorld): no token is checked on
// that path either.
func refuseCrossOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || u.Host == "" || !originIsThisHost(u.Host, r) {
					writeAPIError(w, r, http.StatusForbidden, "cross-origin",
						"This request came from another site's page.", "")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// originIsThisHost reports whether host is the one the browser asked for:
// the request's Host, or the host a proxy in front says it forwarded
// (X-Forwarded-Host), since a proxy that sets its own upstream as the Host
// (CI's screenshot proxy does) would otherwise make every write look like
// another origin's. The header cannot forge a match: a page cannot put it on
// a request the browser sends to another origin without a CORS preflight,
// which this site does not grant.
func originIsThisHost(host string, r *http.Request) bool {
	if strings.EqualFold(host, r.Host) {
		return true
	}
	fwd, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Host"), ",")
	return fwd != "" && strings.EqualFold(host, strings.TrimSpace(fwd))
}
