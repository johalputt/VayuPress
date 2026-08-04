// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
)

// evalRefusedPrefixes are the paths that NEVER get the relaxed policy, whatever
// a domain has opted into.
//
// The opt-in exists for a static page an operator publishes on their own hosted
// domain. It must not follow a visitor into the panel, the API, an OAuth
// consent screen or anything else that carries a session — those are exactly
// the surfaces where 'unsafe-eval' turns a small injection into a large one.
// Matching by prefix rather than by handler is deliberate: a new admin route
// added later inherits the refusal without anyone remembering to add it here.
var evalRefusedPrefixes = []string{
	"/os", "/api", "/admin", "/oauth", "/mcp", "/__vayushield", "/__vayuanalytics",
}

// siteCSPMiddleware relaxes the Content-Security-Policy for a hosted domain that
// has explicitly opted in, and for nothing else.
//
// WHY IT IS A SEPARATE MIDDLEWARE. securityHeadersMiddleware runs before the
// domain is resolved, so at that point there is no way to know which site is
// being served — it can only apply the strict baseline. This runs after
// domainMiddleware, when activeDomain is known, and overwrites the header in the
// one case the operator asked for.
//
// Every condition below has to hold. The default, on every path this does not
// match, stays the strict baseline:
//
//   - a non-primary hosted domain (the primary is the operator's own install)
//   - serving a deployed custom bundle (a template site never needs eval)
//   - with AllowEval set on that domain
//   - on a path that carries no session
func (a *App) siteCSPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.siteAllowsEval(r) {
			hdr := "Content-Security-Policy"
			if config.Cfg.CSPReportOnly {
				hdr = "Content-Security-Policy-Report-Only"
			}
			w.Header().Set(hdr, render.BuildCSPAllowingEval(render.CSPNonce(r)))
		}
		next.ServeHTTP(w, r)
	})
}

// siteAllowsEval reports whether this exact request is the narrow case the
// opt-in covers.
func (a *App) siteAllowsEval(r *http.Request) bool {
	d, ok := activeDomain(r)
	if !ok || d.IsPrimary {
		return false
	}
	site, ok := d.Site()
	if !ok || !site.AllowEval {
		return false
	}
	// A relaxation that applies to a site which is not actually being served
	// from a bundle would be a setting with no visible cause — and it would
	// linger after the operator switched back to a template.
	if !a.customSiteActive(r) {
		return false
	}
	p := r.URL.Path
	for _, pre := range evalRefusedPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return false
		}
	}
	return true
}
