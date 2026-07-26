// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"strings"
	"testing"
)

// TestPricingPageRespectsTheSignedInMember pins the fix for "I am already signed
// in as a member, but the plans page still offers me Get started and Sign in".
//
// The handler builds its markup inline, and exercising it end to end needs a
// members store, a tier catalogue and a live session; the invariants worth
// protecting are the branches themselves, so they are asserted at the source.
func TestPricingPageRespectsTheSignedInMember(t *testing.T) {
	src, err := os.ReadFile("handlers_member_portal.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(src)
	i := strings.Index(s, "func (a *App) handlePricingPage(")
	if i < 0 {
		t.Fatal("handlePricingPage not found")
	}
	body := s[i:]
	if j := strings.Index(body[10:], "\nfunc "); j > 0 {
		body = body[:j+10]
	}

	// It must know who is reading before deciding what to offer.
	if !strings.Contains(body, "a.resolveMember(r)") {
		t.Error("the plans page must resolve the current member")
	}
	// The plan you already hold is stated, not sold.
	if !strings.Contains(body, "Your current plan") {
		t.Error("the member's existing plan must be marked, not offered as a signup")
	}
	// A signed-in free member's whole reason for visiting is the upgrade.
	if !strings.Contains(body, "Upgrade to ") {
		t.Error("a signed-in member on a lower tier must be offered an upgrade")
	}
	// And they must not be invited to sign in or sign up again.
	signedInBlock := body[strings.Index(body, "if signedIn {"):]
	if strings.Contains(signedInBlock[:400], `href="/signup"`) {
		t.Error("a signed-in member must not be sent to /signup")
	}

	// Personalisation and caching: a page that names somebody's plan must never be
	// storable by a shared cache, and the response must declare that it varies on
	// the cookie — otherwise one member's page gets served to everyone.
	if !strings.Contains(body, `w.Header().Set("Vary", "Cookie")`) {
		t.Error("a cookie-dependent page must send Vary: Cookie")
	}
	if !strings.Contains(body, `"private, no-store"`) {
		t.Error("the signed-in variant must be private and unstorable")
	}
	// The anonymous variant is the one search engines index, so it stays cacheable.
	if !strings.Contains(body, `"public, max-age=`) {
		t.Error("the anonymous variant should remain cacheable for speed and SEO")
	}
}

// TestMemberPageStylesStayCalm: the member surfaces are utility pages. Guard the
// states the state-aware plans page depends on, and keep motion dismissable.
func TestMemberPageStylesStayCalm(t *testing.T) {
	b, err := os.ReadFile("../../static/css/signup.css")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	css := string(b)
	for _, want := range []string{".pr-cta--current", ".pr-here", ".pr-card--current"} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %s — the plans page renders it, so it must be styled", want)
		}
	}
	if !strings.Contains(css, "prefers-reduced-motion") {
		t.Error("member page motion must be dismissable")
	}
	if strings.Count(css, "{") != strings.Count(css, "}") {
		t.Error("unbalanced braces in signup.css")
	}
}
