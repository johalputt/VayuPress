// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
)

// ADR-0153 Phase 3 — the scope lives in the URL.
//
// Written from the position of the operator who reported the original defect:
// they want the page they are on to be the site they are editing, and to be
// able to tell which one that is without remembering a mode they set earlier.

// The default for every existing /os page is the primary, explicitly. It must
// be a REAL scope, not an unset one — an unset scope reads product defaults,
// which on the operator's own console would silently blank their site.
func TestAnUnscopedConsolePageMeansThePrimary(t *testing.T) {
	r := httptest.NewRequest("GET", "/os/theme", nil)
	sc := osScope(r)
	if !sc.Valid() {
		t.Fatal("a plain /os page resolved to an UNSET scope, so the operator's own console " +
			"would read product defaults instead of their settings")
	}
	if !sc.IsPrimary() {
		t.Errorf("a plain /os page resolved to %s, want the primary", sc)
	}
}

// A page under /os/d/{id} must act on that domain and nothing else.
func TestAScopedPageActsOnTheDomainInItsPath(t *testing.T) {
	r := httptest.NewRequest("GET", "/os/d/abc123/settings", nil)
	r = withScopeForTest(r, "abc123", "client.example")

	sc := osScope(r)
	if !sc.Valid() || sc.IsPrimary() {
		t.Fatalf("scope = %s, want the hosted domain", sc)
	}
	if sc.DomainID() != "abc123" {
		t.Errorf("scope addresses %q, want abc123", sc.DomainID())
	}
	d, ok := osScopedDomain(r)
	if !ok || d.Host != "client.example" {
		t.Errorf("the resolved domain is %+v, want client.example", d)
	}
}

// The adversarial item from the ADR: a write whose BODY names a different
// domain from its URL must be refused, never silently rescoped.
//
// Silent rescoping reports success for an attempt to edit somebody else's site,
// which hides both the attempt and the bug that produced it.
func TestAWriteNamingADifferentDomainIsRefusedNotRescoped(t *testing.T) {
	r := httptest.NewRequest("POST", "/os/d/abc123/api/settings", nil)
	r = withScopeForTest(r, "abc123", "client.example")

	if !requireScopeMatchesPath(r, "") {
		t.Error("a body naming no domain was refused; the path is the authority and should stand alone")
	}
	if !requireScopeMatchesPath(r, "abc123") {
		t.Error("a body naming its OWN domain was refused")
	}
	if requireScopeMatchesPath(r, "someone-elses-id") {
		t.Fatal("a body naming a DIFFERENT domain was accepted. The write would land on the " +
			"site named in the path while the caller believed it landed elsewhere — or worse, " +
			"be an attempt to edit another client that reported success")
	}
}

// With no scope resolved at all, a body naming any domain must be refused —
// there is nothing to match it against, and matching against "whatever the
// primary is" would be the inheritance defect wearing a different hat.
func TestABodyNamingADomainIsRefusedWhenNoScopeWasResolved(t *testing.T) {
	r := httptest.NewRequest("POST", "/os/api/settings", nil)
	if requireScopeMatchesPath(r, "abc123") {
		t.Error("a domain-naming body was accepted on an unscoped route")
	}
}

// The per-domain routes must not widen what a confined client can reach.
// ADR-0152's audience gate is the boundary; a new route family is exactly the
// kind of change that quietly reopens it.
func TestThePerDomainRoutesAreNotReachableByAConfinedClient(t *testing.T) {
	for _, p := range []string{
		"/os/d/abc123",
		"/os/d/abc123/settings",
		"/os/d/abc123/api/settings",
		"/os/d/abc123/theme",
	} {
		if clientPathAllowed(p) {
			t.Errorf("a confined client can reach %s — the operator's per-domain console is "+
				"not a client surface, and this route family must not be the thing that "+
				"widened the gate", p)
		}
	}
}

// A tool that is not yet scoped must not be linked. Linking it would send the
// operator to a page that edits the PRIMARY while its URL says a hosted domain —
// a worse version of the defect this ADR exists to fix.
func TestUnscopedToolsAreListedButNotLinked(t *testing.T) {
	page := scopedHomePage(testDomain("abc123", "client.example"))
	assertCSPSafe(t, "scopedHomePage", page)

	for _, tool := range scopedTools {
		href := "/os/d/abc123" + tool.Path[len("/os/d/%s"):]
		linked := strings.Contains(page, `href="`+href+`"`)
		if tool.Live && !linked {
			t.Errorf("%s is live but not linked", tool.Title)
		}
		if !tool.Live && linked {
			t.Errorf("%s is NOT scoped yet and is linked anyway. An operator following that "+
				"link edits the primary site from a URL naming a hosted domain", tool.Title)
		}
	}
	// And the page must say why, rather than leaving a dead-looking card.
	if !strings.Contains(page, "would edit the primary") {
		t.Error("the page does not explain why a tool is unavailable, so it reads as broken")
	}
}

// The page must carry the honest ceiling. An operator selling this needs to see
// what is shared in the same view as what is not.
func TestTheScopedConsoleStatesWhatIsShared(t *testing.T) {
	note := scopedIndependenceNote()
	for _, want := range []string{"one process", "row scoping", "fail and recover together"} {
		if !strings.Contains(strings.ToLower(note), strings.ToLower(want)) {
			t.Errorf("the shared-infrastructure note never mentions %q", want)
		}
	}
}

// The save endpoint must write only the keys its own page owns. Without the
// allowlist, any of the 327 keys — including operational ones — could be set
// through a surface that will later face a client.
func TestTheScopedSaveWritesOnlyItsOwnFields(t *testing.T) {
	src := readSourceFile(t, "admin_os_scoped_settings.go")
	body := goFuncBody(src, "handleOSScopedSettingsSave")
	if !strings.Contains(body, "allowed[k]") {
		t.Fatal("the save endpoint has no key allowlist, so a caller can set any setting on " +
			"the install through it")
	}
	if !strings.Contains(body, "osScope(r)") {
		t.Error("the save endpoint does not take its scope from the request path")
	}
	// The scope must never be read from the body.
	if strings.Contains(body, "settings.ForDomain(body.") {
		t.Error("the save endpoint builds its scope from the request BODY, so a caller chooses " +
			"which site they are writing to")
	}
}

// A scoped page must show THIS domain's value, not the primary's — the whole
// visible point of the phase.
func TestTheSettingsPageShowsTheDomainsOwnValues(t *testing.T) {
	src := readSourceFile(t, "admin_os_scoped_settings.go")
	body := goFuncBody(src, "handleOSScopedSettings")
	if !strings.Contains(body, "a.siteSettings.Get(r.Context(), sc,") {
		t.Error("the settings page does not read through the request's scope, so it renders " +
			"the primary's values under a hosted domain's URL")
	}
	if strings.Contains(body, "settings.ForPrimary()") {
		t.Error("the settings page reads the PRIMARY explicitly — it would show the operator " +
			"their own site while claiming to show the client's")
	}
}

// withScopeForTest attaches a resolved scope and domain to a request, the way
// scopedDomainMiddleware does for a real one.
func withScopeForTest(r *http.Request, id, host string) *http.Request {
	ctx := context.WithValue(r.Context(), ctxScopeKey, settings.ForDomain(id))
	ctx = context.WithValue(ctx, ctxScopedDomainKey, testDomain(id, host))
	return r.WithContext(ctx)
}

func testDomain(id, host string) domain.Domain {
	return domain.Domain{ID: id, Host: host, SiteType: domain.SiteBlog, Status: domain.StatusActive}
}
