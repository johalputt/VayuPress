// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/domain"
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
	// The visitor-facing half of "anything else that carries a session", which
	// the list above missed for as long as it existed.
	//
	// vp_member is written with Path "/" (setMemberSessionCookie), so a member
	// session is attached to every path on the host. /api/v1/members/... was
	// already refused by the /api prefix — but the API is not where a script
	// runs. These are the HTML pages, and a page is where eval executes with
	// that cookie in scope.
	//
	// WHAT REFUSING THESE COSTS, stated accurately after the first version of
	// this comment got it wrong. It said refusing them "cannot break an opted-in
	// site: the custom bundle is served at / and as the 404 fallback, so a
	// REGISTERED route was never the operator's own static page to begin with."
	// That reasoning only holds for paths that ARE registered routes. /members,
	// /checkout and /signup are; bare /mail and /vayumail are not — routes.go
	// registers /mail/recover* and /vayumail/privacy and nothing else — so those
	// two, and every unregistered path beneath all five, fall through
	// handleNotFound to the bundle and ARE the operator's own static pages.
	//
	// So a bundle page at /mail, or at /checkout/thanks, now renders under the
	// strict baseline while the rest of the site keeps the relaxation. That is
	// the correct security answer and it is a real cost: vp_member is written
	// with Path "/", so a member session is in scope on exactly those pages, and
	// eval there is what the opt-in must not grant. It is named here rather than
	// denied, because a page that renders inert with nothing explaining why is
	// the failure this file exists to prevent, not one to hide.
	"/members", "/checkout", "/signup", "/mail", "/vayumail",
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
	return siteAllowsEvalGiven(r, a.customSiteActive)
}

// siteAllowsEvalGiven is siteAllowsEval with its one App-dependent condition
// supplied as an argument.
//
// WHY THE SEAM EXISTS, because it is not tidiness. The composition here — "the
// path refusal AND the bundle check" — had no test at all, and the file's own
// comment claimed otherwise. Every assertion called evalPermittedFor directly;
// the single call to siteAllowsEval used &App{} on a request with no resolved
// domain, so it returned at the !ok branch and never reached the refusal it was
// written to guard. Deleting `!evalPermittedFor(d, r.URL.Path)` from the
// condition left the entire suite green, which drops the primary-domain guard,
// the AllowEval opt-in and the path refusal in one edit.
//
// Testing it through the real customSiteActive would need a settings store in
// "custom" mode and a deployed bundle on disk — a fixture heavy enough that the
// test would not have been written, which is how it came to be missing. A
// function parameter is the same answer recovery.go already reached for the mail
// acceptance predicate: "a parameter rather than a package lookup so the rule is
// testable".
func siteAllowsEvalGiven(r *http.Request, bundleActive func(*http.Request) bool) bool {
	d, ok := activeDomain(r)
	if !ok || !evalPermittedFor(d, r.URL.Path) {
		return false
	}
	// A relaxation that applies to a site which is not actually being served
	// from a bundle would be a setting with no visible cause — and it would
	// linger after the operator switched back to a template. This is the one
	// condition that needs App state; every condition above it is decidable
	// from the request alone, which is why they live in evalPermittedFor.
	return bundleActive(r)
}

// evalPermittedFor holds every condition that can be decided from the resolved
// domain and the path, with no App state and no I/O.
//
// It is split out because the conditions used to be inline, and a test could
// then only reach them through siteAllowsEval — which returns false anyway when
// no bundle is deployed. Mutations that deleted the primary-domain guard and the
// path refusal both survived the whole test file for exactly that reason: the
// assertions passed on the strength of a missing bundle, never reaching the
// condition they named. Pure and separate, each one fails a test when removed.
func evalPermittedFor(d domain.Domain, path string) bool {
	// The primary domain is the operator's own install, panel and all.
	if d.IsPrimary {
		return false
	}
	site, ok := d.Site()
	if !ok || !site.AllowEval {
		return false
	}
	// Absolute: no setting reaches past this.
	return !evalRefusedPath(path)
}

// evalRefusedPath reports whether p sits under one of evalRefusedPrefixes.
//
// It is a named function because the tests used to re-implement this loop
// instead of calling it, so they were asserting against a copy of the rule.
// Loosening the real matcher to a bare strings.HasPrefix — which would refuse
// "/mailbox" for merely starting like "/mail" — changed nothing any of them
// could see. A second copy of a rule is a future divergence; a second copy
// living in the test that guards the rule is the divergence already.
//
// The match is whole-segment on purpose: "/mailbox" is not "/mail".
func evalRefusedPath(p string) bool {
	for _, pre := range evalRefusedPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}
