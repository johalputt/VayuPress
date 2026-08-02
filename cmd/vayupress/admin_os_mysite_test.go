// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// The page a client owns must render from the domain THEY are bound to, and
// must not offer them anything the code cannot honour.

func TestMySitePageRendersTheClientsOwnFacts(t *testing.T) {
	d := domain.Domain{
		Host:        "client-a.test",
		TLSState:    domain.TLSActive,
		MailEnabled: true,
		Status:      domain.StatusActive,
	}
	out := mySiteFactsGrid(d)
	for _, want := range []string{"client-a.test", "Secured", "Active"} {
		if !strings.Contains(out, want) {
			t.Errorf("the facts grid does not mention %q:\n%s", want, out)
		}
	}
	// A domain without a certificate must say so and be toned for attention,
	// because "my site shows a warning" is the call the studio wants to pre-empt.
	pending := mySiteFactsGrid(domain.Domain{Host: "b.test", TLSState: domain.TLSPending})
	if !strings.Contains(pending, "Not yet secured") || !strings.Contains(pending, "stat-card--warn") {
		t.Errorf("a domain with no certificate is not flagged:\n%s", pending)
	}
}

// The page must not claim to show traffic. analytics_daily is keyed (day,path)
// with no domain dimension, so two clients sharing /about have MERGED counts —
// a number here would be another client's visits presented as this client's.
func TestMySiteDoesNotShowTrafficItCannotCountPerSite(t *testing.T) {
	out := mySiteWhatsNotHere()
	if !strings.Contains(strings.ToLower(out), "visitor numbers") {
		t.Error("the page does not tell the client why there are no visitor numbers; " +
			"a missing control with no explanation is a support call")
	}
	body := mySiteFactsGrid(domain.Domain{Host: "x.test"}) + out
	for _, forbidden := range []string{"views", "pageviews", "visits this month"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf("the page appears to present a traffic figure (%q) — analytics has no "+
				"domain dimension, so any number shown here includes other clients", forbidden)
		}
	}
}

// Escaping at the render barrier. A domain host and a stored brand are operator
// or client input, and both land in HTML.
func TestMySiteEscapesEverythingItRenders(t *testing.T) {
	// Assert on the RAW payloads, not on fragments of them. `onerror=` also occurs
	// inside the correctly-escaped `&lt;img src=x onerror=1&gt;`, which is inert —
	// matching that reports a hole where there is none, and a test that fails on
	// correct code gets loosened until it fails on nothing.
	hostPayload := `evil"><script>alert(1)</script>`
	namePayload := `"><script>alert(2)</script>`
	tagPayload := `<img src=x onerror=1>`
	d := domain.Domain{Host: hostPayload, Status: domain.StatusActive}
	b := domain.Brand{SiteName: namePayload, Tagline: tagPayload}
	out := mySiteFactsGrid(d) + mySiteBrandCard(d, b)
	for _, raw := range []string{hostPayload, namePayload, tagPayload} {
		if strings.Contains(out, raw) {
			t.Errorf("input reached the page verbatim, so it was never escaped: %q", raw)
		}
	}
	// A raw tag opener is the one extra form worth naming: it catches a payload
	// this test did not think of.
	//
	// There is deliberately NO "attribute closed early" substring check. The
	// obvious one, `value=""><`, matches the legitimately EMPTY description field
	// followed by its hint div — it fails on correct markup, and a test that does
	// that gets loosened until it fails on nothing. Breaking out of an attribute
	// requires an unescaped quote from input, which the verbatim check above
	// already catches.
	if strings.Contains(out, "<script") {
		t.Errorf("a raw <script opener survived:\n%s", out)
	}
}

// House rule: no inline style attributes anywhere in the console's own markup —
// the CSP admits style-src-attr but assertCSPSafe forbids it, and a page that
// breaks it fails the shared gate rather than this test.
func TestMySiteMarkupIsCSPSafe(t *testing.T) {
	d := domain.Domain{Host: "c.test", Status: domain.StatusActive}
	assertCSPSafe(t, "mysite", mySiteFactsGrid(d)+mySiteBrandCard(d, domain.Brand{})+mySiteWhatsNotHere())
}

// The site-mode label must never leak the internal vocabulary, and must default
// to something true rather than something flattering.
func TestMySiteModeLabelIsHonestAndPlain(t *testing.T) {
	cases := map[string]string{"": "Blog", "custom": "Custom design", "business": "Business site"}
	for mode, want := range cases {
		cfg, err := domain.EncodeSiteConfigInto("", domain.SiteConfig{Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		got := mySiteModeLabel(domain.Domain{ConfigJSON: cfg})
		if got != want {
			t.Errorf("mode %q rendered as %q, want %q", mode, got, want)
		}
	}
}

// The brand writer must take the domain from the SESSION and refuse a body that
// names a different one — never silently substitute the caller's own scope.
//
// Silent substitution turns an attempt to write another client's site into a
// success message, which hides the attempt from the operator and the bug from
// whoever wrote it.
func TestBrandSaveRefusesAForeignDomainRatherThanSubstituting(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "admin_os_mysite.go"), "handleOSMySiteBrand")
	if body == "" {
		t.Fatal("handleOSMySiteBrand not found")
	}
	if !strings.Contains(body, "mySiteDomain(r)") {
		t.Error("the handler does not resolve the domain from the session")
	}
	if !strings.Contains(body, "wrong-domain") {
		t.Error("a body naming another domain is not refused. Substituting the caller's own " +
			"scope instead reports success for an attempt to write someone else's site")
	}
	if !strings.Contains(body, "SetBrand") {
		t.Error("the handler does not go through SetBrand, which is what merges into " +
			"config_json rather than replacing it")
	}
}

// A client bound to the primary domain, or to a disabled one, must not get a
// page. The primary is the agency's own install.
func TestMySiteRefusesThePrimaryAndDisabledDomains(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "admin_os_mysite.go"), "mySiteDomain")
	if body == "" {
		t.Fatal("mySiteDomain not found")
	}
	if !strings.Contains(body, "IsPrimary") {
		t.Error("mySiteDomain does not refuse the primary domain — a client bound to it " +
			"would administer the agency's own site")
	}
	if !strings.Contains(body, "StatusActive") {
		t.Error("mySiteDomain does not check the domain is active, so a soft-deleted site " +
			"renders as though it were live")
	}
	if !strings.Contains(body, "clientScopeFor") {
		t.Error("mySiteDomain does not resolve through clientScopeFor, so an invalid " +
			"binding is not refused")
	}
}
