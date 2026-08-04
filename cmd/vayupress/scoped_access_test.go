// SPDX-License-Identifier: Apache-2.0

package main

// scoped_access_test.go — who may open the per-domain console.
//
// Found by attacking the pages the house-style conversion touched, rather than
// by reviewing what the conversion changed. The conversion itself was fine; the
// namespace those pages live in was not.
//
// osPathMinLevel has two gates and this namespace fell between them.
// osPathInArea matches `/os/<area>` and `/os/api/<area>`, so `/os/d/x/settings`
// matched no area at all and dropped to the permissive author default. The
// fail-closed API rule that exists to catch exactly that only fires on paths
// beginning `/os/api/`, and none of the per-domain APIs do.
//
// The consequence was that mounting a page under a domain LOWERED its gate.

import "testing"

// The attack: sign in as an author, open another customer's domain console, and
// POST theme code to their live site.
//
// /os/api/theme/code requires editor. The SAME handler mounted at
// /os/d/{id}/api/theme/code required author and carries no role check of its
// own, so an author could write site-wide custom CSS onto any hosted customer's
// domain. Reads were worse in breadth and better in consequence: every hosted
// site's settings, content list, visitor figures, certificate state and uploaded
// bundle manifest were author-readable.
func TestMountingAPageUnderADomainNeverLowersItsGate(t *testing.T) {
	for _, c := range []struct{ primary, scoped string }{
		{"/os/settings", "/os/d/abc/settings"},
		{"/os/api/settings", "/os/d/abc/api/settings"},
		{"/os/website", "/os/d/abc/website"},
		{"/os/theme", "/os/d/abc/theme"},
		{"/os/api/theme/code", "/os/d/abc/api/theme/code"},
		{"/os/seo", "/os/d/abc/seo"},
		{"/os/analytics", "/os/d/abc/analytics"},
	} {
		if got, want := osPathMinLevel(c.scoped), osPathMinLevel(c.primary); got < want {
			t.Errorf("%s requires level %d but %s requires only %d — mounting the page under a "+
				"domain handed it to a lower role than can reach it at its own address",
				c.primary, want, c.scoped, got)
		}
	}
}

// And the namespace as a whole. Every page under it administers a HOSTED
// CUSTOMER'S site and is reached from the Domains registry, which is admin-only;
// there is no route into it that an author is meant to have.
//
// Stated as a rule over the whole prefix rather than page by page, because the
// per-page version is what failed: each new per-domain route inherited the
// author default silently, and nothing anywhere reported it.
func TestEveryPerDomainConsolePathIsAdminOnly(t *testing.T) {
	for _, p := range []string{
		"/os/d/abc",
		"/os/d/abc/settings", "/os/d/abc/content", "/os/d/abc/website",
		"/os/d/abc/theme", "/os/d/abc/seo", "/os/d/abc/analytics",
		"/os/d/abc/api/settings", "/os/d/abc/api/theme/code",
		"/os/d/abc/api/copy-from-primary", "/os/d/abc/api/website/bundle",
		"/os/d/abc/api/website/bundle/rollback", "/os/d/abc/api/content/move",
		"/os/d/abc/api/content/new", "/os/d/abc/api/website/preview",
		// A route nobody has written yet. The rule has to hold for the NEXT
		// per-domain endpoint too, since inheriting the author default silently is
		// the failure being closed.
		"/os/d/abc/api/something-added-later",
	} {
		if got := osPathMinLevel(p); got != accessAdmin {
			t.Errorf("%s requires level %d, want admin (%d): a console user below admin can reach "+
				"another customer's site through it", p, got, accessAdmin)
		}
	}
}

// The rule must not spill onto unrelated paths. `/os/domains` and `/os/dns` are
// already admin for their own reasons, but a prefix test written carelessly
// would also swallow any future `/os/dashboard` — matching "/os/d" as a bare
// prefix rather than a path segment.
func TestThePerDomainRuleDoesNotSwallowNeighbouringPaths(t *testing.T) {
	for _, p := range []string{"/os/dashboard", "/os/drafts", "/os/api/diagram"} {
		if osPathMinLevel(p) == accessAdmin && p == "/os/dashboard" {
			t.Errorf("%s was caught by the per-domain prefix rule and locked to admin; it is an "+
				"ordinary author page", p)
		}
	}
	// The author-safe content APIs must stay author-safe.
	if got := osPathMinLevel("/os/api/diagram"); got != accessAuthor {
		t.Errorf("/os/api/diagram requires level %d, want author (%d)", got, accessAuthor)
	}
}
