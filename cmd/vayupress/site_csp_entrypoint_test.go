// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// FINDING (post-v3.17.48) — the entry point had no test, and its own comment
// said it did.
//
// site_csp_test.go claimed to "exercise siteAllowsEval, not the matcher it
// calls", and that without it "siteAllowsEval could stop consulting the refusal
// list altogether and nothing would fail". Its single call passed &App{} on a
// request carrying no resolved domain, so activeDomain returned ok=false and the
// function returned before evalPermittedFor was ever reached. Every other
// assertion called evalPermittedFor directly.
//
// The mutation that proves it: deleting `!evalPermittedFor(d, r.URL.Path)` from
// the condition left the WHOLE suite green. That single edit drops three guards
// at once — the primary-domain guard, the AllowEval opt-in, and the path refusal
// — so an install with a bundle deployed would serve 'unsafe-eval' on /os/*, on
// /members/account, and on the operator's own primary domain, which never opted
// in. The file written to prevent exactly that reported clean.

// requestForDomain builds a request carrying a resolved domain, the way
// domainMiddleware does, so the entry point can be driven at all.
func requestForDomain(d domain.Domain, path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	return r.WithContext(context.WithValue(r.Context(), ctxKeyDomain{}, d))
}

// evalOptedInDomain is a hosted domain that has genuinely opted in, so the only
// thing left to decide is the path.
func evalOptedInDomain(t *testing.T) domain.Domain {
	t.Helper()
	cfg, err := domain.EncodeSiteConfigInto("", domain.SiteConfig{Mode: "custom", AllowEval: true})
	if err != nil {
		t.Fatalf("encode site config: %v", err)
	}
	d := domain.Domain{ID: "abc123", Host: "client.example", ConfigJSON: cfg}
	if sc, ok := d.Site(); !ok || !sc.AllowEval {
		t.Fatalf("test setup is wrong: the domain does not carry AllowEval (ok=%v %+v)", ok, sc)
	}
	return d
}

// bundleIsDeployed stands in for customSiteActive. Always true, because the
// point is to reach the conditions BEHIND it: with the real one, a missing
// bundle short-circuits every assertion and the test proves nothing — which is
// precisely how the gap arose.
func bundleIsDeployed(*http.Request) bool { return true }

func TestTheEntryPointItselfConsultsTheRefusalList(t *testing.T) {
	d := evalOptedInDomain(t)

	// The opted-in site's own page still gets the relaxation, or this test
	// passes by refusing everything and proves nothing.
	if !siteAllowsEvalGiven(requestForDomain(d, "/"), bundleIsDeployed) {
		t.Fatal("an opted-in domain serving a deployed bundle was refused at its own root, so " +
			"every assertion below would hold against a function that always returns false")
	}

	// Each refused prefix, driven through the ENTRY POINT rather than through
	// the matcher it calls.
	for _, p := range []string{
		"/os", "/os/login", "/api/v1/admin/users", "/oauth/authorize",
		"/members/account", "/checkout", "/mail", "/vayumail",
	} {
		if siteAllowsEvalGiven(requestForDomain(d, p), bundleIsDeployed) {
			t.Errorf("the entry point granted 'unsafe-eval' on %s. It is not enough for "+
				"evalRefusedPath to know: siteAllowsEval has to ask it", p)
		}
	}
}

// The other two guards live behind the same call and were equally unreached.
func TestTheEntryPointStillHonoursThePrimaryGuardAndTheOptIn(t *testing.T) {
	primary := evalOptedInDomain(t)
	primary.IsPrimary = true
	if siteAllowsEvalGiven(requestForDomain(primary, "/"), bundleIsDeployed) {
		t.Error("the primary domain — the operator's own install, panel and all — was granted " +
			"the relaxation through the entry point")
	}

	configured, err := domain.EncodeSiteConfigInto("", domain.SiteConfig{Mode: "custom", AllowEval: false})
	if err != nil {
		t.Fatalf("encode site config: %v", err)
	}
	off := domain.Domain{ID: "def456", Host: "other.example", ConfigJSON: configured}
	if siteAllowsEvalGiven(requestForDomain(off, "/"), bundleIsDeployed) {
		t.Error("a CONFIGURED site that left eval off was granted it anyway — the case that " +
			"distinguishes 'no config' from 'config saying no'")
	}

	// And a request with no resolved domain at all.
	if siteAllowsEvalGiven(httptest.NewRequest(http.MethodGet, "/", nil), bundleIsDeployed) {
		t.Error("a request with no resolved domain was granted the relaxation")
	}
}

// The bundle check must still gate it, or the seam has quietly removed the
// condition it was extracted to keep.
func TestTheEntryPointStillRequiresADeployedBundle(t *testing.T) {
	d := evalOptedInDomain(t)
	if siteAllowsEvalGiven(requestForDomain(d, "/"), func(*http.Request) bool { return false }) {
		t.Error("a domain with no deployed bundle was granted the relaxation, so the setting " +
			"applies with no visible cause and lingers after a switch back to a template")
	}
}

// The production entry point must go through the seam, or all of the above
// tests a function nothing calls.
func TestSiteAllowsEvalGoesThroughTheTestedComposition(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "middleware_site_csp.go"), "siteAllowsEval")
	if body == "" {
		t.Fatal("siteAllowsEval is gone")
	}
	if !strings.Contains(body, "siteAllowsEvalGiven(r, a.customSiteActive)") {
		t.Fatal("siteAllowsEval no longer delegates to siteAllowsEvalGiven, so the composition " +
			"the tests above drive is not the one that ships")
	}
}
