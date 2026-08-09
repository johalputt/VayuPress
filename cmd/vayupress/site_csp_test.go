// SPDX-License-Identifier: Apache-2.0

package main

// site_csp_test.go — the per-domain opt-out of the no-eval rule.
//
// It exists because the strict baseline made a whole class of site impossible
// rather than merely awkward: mainstream front-end libraries compile the
// expression strings written in markup into functions at runtime, the policy
// refuses that, and the page renders inert with nothing explaining why.
//
// The opt-in is therefore a real product need. It is also a real loosening, so
// the value of this file is entirely in what it proves the relaxation CANNOT
// reach. A setting that quietly widened the policy for the panel would be worse
// than never having offered it.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
)

// The relaxed policy must differ from the baseline in exactly one directive.
// Anything else that drifted in would be a second, unrequested loosening riding
// along with the one the operator agreed to.
func TestTheRelaxedPolicyChangesScriptSrcAndNothingElse(t *testing.T) {
	const nonce = "testnonce"
	base := render.BuildCSP(nonce, nil)
	eased := render.BuildCSPAllowingEval(nonce)

	if base == eased {
		t.Fatal("the relaxed policy is identical to the baseline, so the opt-in does nothing")
	}
	if !strings.Contains(eased, "'unsafe-eval'") {
		t.Fatal("the relaxed policy does not actually admit eval, so the library it exists for " +
			"still will not run")
	}
	if strings.Contains(base, "'unsafe-eval'") {
		t.Fatal("the BASELINE admits eval — the opt-in is meaningless because every site " +
			"already has it")
	}

	// Compare directive by directive. Only script-src may differ.
	split := func(s string) map[string]string {
		out := map[string]string{}
		for _, d := range strings.Split(s, ";") {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			if i := strings.Index(d, " "); i > 0 {
				out[d[:i]] = d[i+1:]
			} else {
				out[d] = ""
			}
		}
		return out
	}
	b, e := split(base), split(eased)
	if len(b) != len(e) {
		t.Fatalf("the relaxed policy has a different set of directives (%d vs %d)", len(b), len(e))
	}
	for k, bv := range b {
		ev, ok := e[k]
		if !ok {
			t.Errorf("directive %q disappeared from the relaxed policy", k)
			continue
		}
		if k == "script-src" {
			continue
		}
		if bv != ev {
			t.Errorf("directive %q changed and should not have:\n  baseline: %s\n  relaxed:  %s", k, bv, ev)
		}
	}

	// The sources themselves must stay 'self' — eval is the concession, not a
	// licence to pull code from anywhere.
	//
	// Asserted as a WHITELIST of permitted tokens rather than a search for
	// suspicious ones. The first version looked for "https://" and a mutation
	// that added the bare scheme source `https:` sailed straight past it: one
	// character short of the pattern, and a policy that admits every host on the
	// web would have been reported as unchanged.
	for _, tok := range strings.Fields(e["script-src"]) {
		switch {
		case tok == "'self'", tok == "'unsafe-eval'":
		case strings.HasPrefix(tok, "'nonce-"), strings.HasPrefix(tok, "'sha256-"),
			strings.HasPrefix(tok, "'sha384-"), strings.HasPrefix(tok, "'sha512-"):
		default:
			t.Errorf("script-src carries an unexpected source %q. The opt-in concedes eval and "+
				"nothing else — a host or scheme source here would let the page load code from "+
				"somewhere the operator never agreed to.\n  full directive: %s", tok, e["script-src"])
		}
	}
}

// THE POINT OF THE WHOLE FILE. The opt-in belongs to one static site on one
// hosted domain. It must never follow a visitor into a surface that carries a
// session, where eval turns a small injection into account takeover.
func TestTheRelaxationNeverReachesAnAuthenticatedSurface(t *testing.T) {
	mustRefuse := []string{
		"/os", "/os/", "/os/settings", "/os/api/shield/rescue",
		"/api", "/api/v1/members/me",
		"/admin", "/admin/theme",
		"/oauth", "/oauth/authorize",
		"/mcp", "/mcp/messages",
		"/__vayushield/pow", "/__vayuanalytics/enter",
	}
	for _, p := range mustRefuse {
		if !evalRefusedPath(p) {
			t.Errorf("%q would receive the relaxed policy. A session lives behind that path, and "+
				"'unsafe-eval' there converts an injected string into full control of the "+
				"operator's account", p)
		}
	}
}

// And it must still apply to the thing it was built for, or the feature is a
// refusal wearing a setting's clothes.
func TestAnOrdinarySitePathIsNotRefused(t *testing.T) {
	for _, p := range []string{"/", "/index.html", "/assets/app.js", "/assets/site.css", "/about"} {
		if evalRefusedPath(p) {
			t.Errorf("%q is refused, so the opted-in site still cannot run", p)
		}
	}
}

// A prefix match must not catch a path that merely STARTS with those letters.
// "/oscar" is not "/os", and refusing it would break an innocent page for a
// reason nobody could find.
func TestThePrefixMatchDoesNotOverreach(t *testing.T) {
	for _, p := range []string{"/oscar", "/apiary", "/administration", "/mcpherson",
		// The visitor-facing prefixes added later. A bundle may legitimately own
		// any of these, and the matcher must require a whole segment: "/mailbox"
		// is not "/mail", and refusing it would break a page for a reason nobody
		// could find.
		"/mailbox", "/mailing-list", "/members-only", "/membership",
		"/checkouts", "/signups", "/vayumailer"} {
		if evalRefusedPath(p) {
			t.Errorf("%q was refused — a legitimate page broken by an over-eager match", p)
		}
	}
}

// TestTheRelaxationNeverReachesASessionBEARINGPage is the test the one above
// should have been.
//
// TestTheRelaxationNeverReachesAnAuthenticatedSurface enumerates only paths that
// already sit under the seven refused prefixes, so it asserts that the list
// contains what the list contains. It passes whatever else carries a session.
//
// These do. vp_member is written with Path "/" (handlers_portal.go), so the
// member session cookie is attached to every path on that host — including these
// HTML pages, which are where a script actually runs in the browser. The member
// API under /api/v1/members/... was already refused by the /api prefix, but the
// API is not where the script executes; the page is.
//
// The comment on evalRefusedPrefixes states the rule as "the panel, the API, an
// OAuth consent screen or anything else that carries a session". This is the
// "anything else".
func TestTheRelaxationNeverReachesASessionBearingPage(t *testing.T) {
	// Registered routes, not bundle pages: the custom bundle is served at "/"
	// and as the 404 fallback, so a registered path is never the operator's own
	// static page and refusing it cannot break a site that worked.
	sessionPages := []string{
		"/members",                // member sign-in
		"/members/account",        // member account — reads and writes the session
		"/checkout",               // payment
		"/checkout/success",       // payment return, fulfils server-side
		"/checkout/paypal/return", // ditto
		"/checkout/crypto/return", // ditto
		"/signup",                 // account creation
		"/mail",                   // webmail
		"/vayumail",               // webmail
	}
	for _, p := range sessionPages {
		if !evalRefusedPath(p) {
			t.Errorf("%q would receive 'unsafe-eval'. The member session cookie is Path=\"/\", so "+
				"it is attached to this page, and eval there turns an injected string into "+
				"account takeover", p)
		}
	}
}

// TestTheMiddlewareItselfRefusesAnAuthenticatedPath exercises siteAllowsEval,
// not the matcher it calls.
//
// Every other test in this file asserts against evalRefusedPath. That leaves the
// entry point unguarded: siteAllowsEval could stop consulting the refusal list
// altogether and nothing would fail — a mutation returning true unconditionally
// passed the whole file. This is the test that sees it.
//
// It needs no deployed bundle because the path refusal is now evaluated before
// customSiteActive, so a request that must be refused is refused on the strength
// of its path alone.
func TestTheMiddlewareItselfRefusesAnAuthenticatedPath(t *testing.T) {
	optedIn := domain.Domain{
		ID: "abc123", Host: "client.example", IsPrimary: false, Status: "active",
	}
	cfg, err := domain.EncodeSiteConfigInto("", domain.SiteConfig{Mode: "custom", AllowEval: true})
	if err != nil {
		t.Fatalf("encode site config: %v", err)
	}
	optedIn.ConfigJSON = cfg
	if s, ok := optedIn.Site(); !ok || !s.AllowEval {
		t.Fatalf("test setup is wrong: the domain does not carry AllowEval (%+v)", s)
	}

	// A domain that has opted in, on paths that must never be relaxed.
	for _, p := range []string{"/os/settings", "/api/v1/members/me", "/members/account", "/checkout"} {
		if evalPermittedFor(optedIn, p) {
			t.Errorf("eval permitted on %q for an opted-in domain", p)
		}
	}

	// The site it was built for still works, or the feature is a refusal
	// wearing a setting's clothes.
	if !evalPermittedFor(optedIn, "/assets/app.js") {
		t.Error("eval refused on an ordinary bundle path, so the opt-in does nothing")
	}

	// The primary domain is never relaxed, whatever it carries.
	primary := optedIn
	primary.IsPrimary = true
	if evalPermittedFor(primary, "/") {
		t.Error("eval permitted on the PRIMARY domain — the operator's own install")
	}

	// A domain with NO site config at all.
	bare := optedIn
	bare.ConfigJSON = ""
	if evalPermittedFor(bare, "/") {
		t.Error("eval permitted for a domain carrying no site config")
	}

	// And the case that actually describes every configured client site: it HAS
	// a site config, mode and all, and simply left the eval switch off. This is
	// the default state, so it is the one that matters most — and it is distinct
	// from the bare case above, because here d.Site() succeeds and only the flag
	// says no. A guard that collapsed those two (|| becoming &&) relaxed the
	// policy for every configured site that never asked for it.
	configuredButOff, err := domain.EncodeSiteConfigInto("", domain.SiteConfig{Mode: "custom"})
	if err != nil {
		t.Fatalf("encode site config: %v", err)
	}
	off := optedIn
	off.ConfigJSON = configuredButOff
	if s, ok := off.Site(); !ok || s.AllowEval {
		t.Fatalf("test setup is wrong: want a readable site config with AllowEval off, got ok=%v %+v", ok, s)
	}
	if evalPermittedFor(off, "/") {
		t.Error("eval permitted for a configured domain that left AllowEval off")
	}

	// And the middleware entry point must still refuse when no domain resolved.
	a := &App{}
	if a.siteAllowsEval(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Error("siteAllowsEval allowed eval with no resolved domain")
	}
}
