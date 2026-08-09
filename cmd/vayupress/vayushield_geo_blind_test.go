// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// "I have blocked visitors from Singapore but I am still getting traffic from
// there."
//
// Nothing was misconfigured. The site is proxied, the origin was not resolving
// the reader's address, and the enforcement path therefore looked up the country
// of the CDN's edge for every proxied visitor. A denied country could not match,
// because the shield was never shown one.
//
// The operator had no way to see that. The field accepted the code, the shield
// reported itself protecting, and Analytics — which reads the country from the
// CDN's own header, and was right — went on showing the traffic they had
// refused. Two of their own screens disagreed and neither explained why.
//
// So the warning belongs on the field, not only in the posture report. The
// posture row is where an operator goes to audit; the field is where they are
// standing when they form the belief that the country is now blocked.

// geoBlindRequest builds a request that arrives with no resolvable visitor
// address — the peer is all there is.
func geoBlindRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/os/shield", nil)
	r.RemoteAddr = "198.51.100.20:44321"
	return r
}

// forgetCDNSightings clears the process-wide proxy observation.
//
// Not hygiene — correctness. shieldResolvesVisitorIP consults it deliberately,
// so a sighting recorded by an unrelated test travels into this one and silently
// flips the answer to "resolving fine".
func forgetCDNSightings(t *testing.T) {
	t.Helper()
	prevAt, prevVendor := cdnSeenUnix.Load(), lastCDNObservation()
	cdnSeenUnix.Store(0)
	t.Cleanup(func() {
		cdnSeenUnix.Store(prevAt)
		cdnSeenVendor.Store(prevVendor)
	})
}

func TestTheGeoFieldsSaySoWhenTheShieldCannotSeeTheVisitor(t *testing.T) {
	forgetCDNSightings(t)
	a := newShieldApp(t, "on")
	r := geoBlindRequest()

	if !a.shieldGeoIsBlind(r) {
		t.Fatal("a proxied install whose requests do not resolve to a visitor address is not " +
			"reported as geo-blind, so no country rule will ever warn about itself")
	}

	page := a.shieldPolicyBand(t.Context(), true)

	// All three country fields, because all three are compared against the same
	// lookup. Warning on the deny list alone would leave an operator believing
	// their "serve ONLY these countries" allowlist was holding — which fails
	// OPEN, and is the worse of the two.
	for _, field := range []string{"sh_deny_countries", "sh_challenge_countries", "sh_allow_countries"} {
		note := noteForField(t, page, field)
		if !strings.Contains(note, "Not being applied right now") {
			t.Errorf("%s does not say it is inert.\n\nThe operator types a country here and the "+
				"page accepts it while nothing enforces it.\n\nnote: %s", field, note)
		}
	}

	// The three things the operator cannot act without: that it is their CDN
	// being located rather than the reader, why Analytics disagrees, and which
	// control fixes it. Named separately — a single substring match would pass on
	// a warning that scolds without telling anyone what to do.
	if !strings.Contains(page, "Analytics") {
		t.Error("the warning never explains why Analytics still reports the refused country, " +
			"which is the contradiction that prompted this report in the first place")
	}
	if !strings.Contains(page, "Resolve the visitor&rsquo;s real address") &&
		!strings.Contains(page, "Resolve the visitor's real address") {
		t.Error("the warning does not name the control that fixes it, so it is a diagnosis with " +
			"no remedy — the defect the Real visitor IP row already had once")
	}
}

// The negative half, and the one that keeps the warning worth reading.
func TestTheGeoWarningIsAbsentWhenTheShieldCanSeeTheVisitor(t *testing.T) {
	forgetCDNSightings(t)

	// A proxied install where resolution WORKS: the peer is a trusted proxy and
	// hands over a distinct visitor address, exactly as nginx does once it is
	// told to resolve the reader.
	prev := config.Cfg.TrustedProxies
	_, loop, _ := net.ParseCIDR("127.0.0.0/8")
	config.Cfg.TrustedProxies = []*net.IPNet{loop}
	t.Cleanup(func() { config.Cfg.TrustedProxies = prev })

	resolving := httptest.NewRequest(http.MethodGet, "/os/shield", nil)
	resolving.RemoteAddr = "127.0.0.1:8443"
	resolving.Header.Set("X-Real-IP", "203.0.113.77")

	a := newShieldApp(t, "on")
	if a.shieldGeoIsBlind(resolving) {
		t.Error("a proxied install that resolves the visitor is reported as geo-blind; the rules " +
			"work and the panel says they do not")
	}

	// And an ordinary un-proxied install. Traffic arrives directly, so the peer
	// IS the visitor and geography is exactly as accurate as it ever gets. A
	// warning here would be a red row that is wrong on every install that never
	// had a CDN — which is how an operator learns to stop reading them.
	direct := newShieldApp(t, "")
	if direct.shieldGeoIsBlind(geoBlindRequest()) {
		t.Error("an install with no CDN in front is told its country rules are inert")
	}

	if strings.Contains(a.shieldPolicyBand(t.Context(), false), "Not being applied right now") {
		t.Error("the policy band renders the inert warning when nothing is inert")
	}
}

// ONE ANSWER, TWO SCREENS.
//
// This is the defect underneath the defect. The posture report could already
// detect the failure; the policy band asked nothing and rendered a rule as
// active. Both now derive from shieldResolvesVisitorIP, and the reason to pin it
// structurally is that the divergence returns the moment somebody re-computes
// the answer locally — which is precisely how it arose.
func TestBothScreensDeriveTheAnswerFromOneFunction(t *testing.T) {
	src := readSourceFile(t, "vayushield_audit.go")

	// The verdict is reached in exactly one place. Named for what it decides
	// rather than for the comparison it happens to use — the first version of
	// this test keyed on the literal expression `auth.ClientIP(r) !=
	// stripPort(r.RemoteAddr)`, which went red the moment that expression was
	// replaced by a CORRECT one, so it was guarding a spelling rather than the
	// property.
	if strings.Count(src, "func shieldAddressIsTheReaders(") != 1 {
		t.Error("the verdict is not reached in exactly one function.\n\n" +
			"Two copies are two answers: the posture report said the address was not resolving " +
			"while the policy band showed a country rule as enforcing, and nothing connected them.")
	}
	// Exactly one CALL site. The definition reads
	// `shieldAddressIsTheReaders(r *http.Request)` and so does not match this
	// literal — checked rather than assumed, because an off-by-one here would
	// make the assertion unfalsifiable in one direction.
	if n := strings.Count(src, "shieldAddressIsTheReaders(r)"); n != 1 {
		t.Errorf("the verdict is called from %d places, want 1 — every consumer must reach it "+
			"through shieldResolvesVisitorIP, or the two screens can drift apart again", n)
	}
	if !strings.Contains(src, "in.ClientIPResolved, in.ClientIPFromVisitorTraffic = shieldResolvesVisitorIP(r)") {
		t.Error("the posture report no longer sources ClientIPResolved from shieldResolvesVisitorIP")
	}
	if !strings.Contains(src, "resolved, _ := shieldResolvesVisitorIP(r)") {
		t.Error("shieldGeoIsBlind no longer sources its answer from shieldResolvesVisitorIP")
	}
}

// noteForField returns the descriptive note rendered for one textarea.
//
// Scoped to the field rather than searched for across the page: a page-wide
// match would pass with the warning attached to a single field, which is the
// half-fix this test exists to refuse.
func noteForField(t *testing.T, page, field string) string {
	t.Helper()
	i := strings.Index(page, field)
	if i < 0 {
		t.Fatalf("field %q is not on the page", field)
	}
	// vsArea renders label, textarea, then the note, all inside one
	// <div class="vs-field …>. Bounding at the NEXT such div keeps one field's
	// warning from answering for another's — without that, a single warning
	// anywhere on the page satisfies all three assertions.
	rest := page[i:]
	if j := strings.Index(rest, `<div class="vs-field`); j >= 0 {
		rest = rest[:j]
	}
	return rest
}
