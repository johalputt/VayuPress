// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scoped_recheck_test.go — a page that says "run again" must offer it.
//
// THE REPORT, verbatim in substance: "I have pointed the domain to the server
// but on the server it is not showing, there is no option for refresh, so I
// deleted it from VayuPress and re-added it — that works perfectly."
//
// Deleting a domain to refresh a status line is the panel admitting it has no
// refresh. The diagnostic's own copy said "point it at this server, then run
// again" and there was no control anywhere on the page that ran anything.

import (
	"strings"
	"testing"
	"time"
)

func recheckChecks(dnsOK bool) []diagCheck {
	return []diagCheck{
		{Label: "Root-side helper installed", Detail: "present", OK: true},
		{Label: "DNS answers for haru.example", Detail: "the name does not resolve", OK: dnsOK, Fatal: !dnsOK},
	}
}

// THE test. The control exists, targets the panel, and re-runs the checks.
func TestTheDiagnosticOffersAWayToRunItAgain(t *testing.T) {
	panel := scopedDiagnosticPanel("dom-1", recheckChecks(false), nil, "haru.example", time.Now())

	if !strings.Contains(panel, "Re-check now") {
		t.Fatalf("the diagnostic has no re-check control. Its own copy tells an operator to run "+
			"again, and the only ways to do that were reloading the browser or requesting a whole "+
			"root-side provisioning run:\n%s", panel)
	}
	if !strings.Contains(panel, `hx-get="/os/d/dom-1/diagnose/live"`) {
		t.Errorf("the re-check does not call this domain's own endpoint:\n%s", panel)
	}
	if !strings.Contains(panel, `hx-target="#`+scopedDiagnosticPanelID) {
		t.Error("the re-check does not target the panel, so pressing it would not update what the " +
			"operator is reading")
	}
	if !strings.Contains(panel, `id="`+scopedDiagnosticPanelID+`"`) {
		t.Error("the panel carries no id for the swap to land on")
	}
}

// "Is this stale?" must be answerable without guessing.
func TestThePanelStampsWhenItRan(t *testing.T) {
	at := time.Date(2026, 8, 6, 9, 41, 7, 0, time.UTC)
	panel := scopedDiagnosticPanel("dom-1", recheckChecks(false), nil, "haru.example", at)
	if !strings.Contains(panel, "09:41:07 UTC") {
		t.Errorf("the panel does not say when it ran, so an operator cannot tell a fresh answer "+
			"from one rendered ten minutes ago:\n%s", panel)
	}
}

// THE honesty test. A re-check can legitimately return the same answer, because
// the machine's resolver caches a negative reply for minutes and nothing in
// this process can clear somebody else's cache. Saying so is what stops an
// operator concluding the button is broken — and reaching for delete-and-re-add
// again.
func TestWhenDNSIsTheBlockerThePanelExplainsWhyRecheckingMayNotHelpYet(t *testing.T) {
	blocked := scopedDiagnosticPanel("dom-1", recheckChecks(false), nil, "haru.example", time.Now())
	if !strings.Contains(blocked, "caches") {
		t.Errorf("DNS is the blocker and the panel does not mention resolver caching. An operator "+
			"who just fixed their record will press re-check, see no change, and conclude the "+
			"control does nothing:\n%s", blocked)
	}
	if !strings.Contains(blocked, "Nothing in this console can clear that cache") {
		t.Error("the panel does not admit the limit; implying the button can force a fresh answer " +
			"is a claim the product cannot keep")
	}

	// And it must NOT say it when DNS is fine — advice for a problem an operator
	// does not have is the noise that makes the rest unread.
	okPanel := scopedDiagnosticPanel("dom-1", recheckChecks(true), nil, "haru.example", time.Now())
	if strings.Contains(okPanel, "caches") {
		t.Errorf("the resolver-cache note is shown when DNS already resolves:\n%s", okPanel)
	}
}

// The DNS row must be identified by its own label, not by a substring that any
// neighbouring row's advice text could satisfy.
func TestTheDNSRowIsIdentifiedByItsOwnLabel(t *testing.T) {
	if !isDNSCheck("DNS answers for haru.example") {
		t.Error("the real DNS row is not recognised")
	}
	// Every one of these MENTIONS DNS or looks close to the row, and none of
	// them is it. A substring match for "DNS" accepts most of this list, which
	// is how the loose version survived its first mutation: the caveat about
	// resolver caching would appear on a page where DNS resolves perfectly.
	for _, other := range []string{
		"This server can answer its own challenge",
		"nginx has NO server block for haru.example",
		"Listed for provisioning",
		"Your DNS provider is not supported",
		"Check the DNS records on Domains & DNS",
		"DNSSEC is enabled for this zone",
		"Custom DNS resolver configured",
		"",
		"DNS",
	} {
		if isDNSCheck(other) {
			t.Errorf("%q was mistaken for the DNS result row. The resolver-cache caveat is shown "+
				"only when DNS is the blocker, so a false match puts advice on a page that does "+
				"not have that problem.", other)
		}
	}
}

// The inline render and the endpoint's fragment must be the same shape, or the
// first swap visibly rearranges the page for no reason the operator asked for.
func TestTheSwappedFragmentMatchesTheInlineRender(t *testing.T) {
	at := time.Now()
	a := scopedDiagnosticPanel("dom-1", recheckChecks(false), nil, "haru.example", at)
	b := scopedDiagnosticPanel("dom-1", recheckChecks(false), nil, "haru.example", at)
	if a != b {
		t.Error("two renders of the same state differ; the swap would change the page unpredictably")
	}
	if !strings.HasPrefix(strings.TrimSpace(a), `<div id="`+scopedDiagnosticPanelID+`"`) {
		t.Errorf("the fragment does not start with the swap target, so hx-swap=outerHTML would "+
			"nest a panel inside itself on every press:\n%s", a[:120])
	}
}

// House style and CSP.
func TestTheRecheckPanelIsCSPSafe(t *testing.T) {
	assertCSPSafe(t, "recheck panel", scopedDiagnosticPanel("dom-1", recheckChecks(false), nil, "haru.example", time.Now()))
	assertCSPSafe(t, "recheck panel ok", scopedDiagnosticPanel("dom-1", recheckChecks(true), nil, "haru.example", time.Now()))
}

// A hostile host or domain id must render as text.
func TestTheRecheckPanelEscapesItsInputs(t *testing.T) {
	panel := scopedDiagnosticPanel(`d"><img onerror=alert(1) src=x>`, recheckChecks(false),
		nil, `h"><script>bad()</scr`+`ipt>`, time.Now())
	if strings.Contains(panel, "<img onerror") {
		t.Errorf("the domain id reached the page as markup:\n%s", panel)
	}
	if strings.Contains(panel, "<scr"+"ipt>bad()") {
		t.Errorf("the host reached the page as markup:\n%s", panel)
	}
}

// The route must exist and be a GET — it changes nothing, so requiring a CSRF
// token would make a read fail for a reason an operator cannot act on.
func TestTheRecheckRouteIsRegisteredAsARead(t *testing.T) {
	src := readSourceFile(t, "admin_os_ui.go")
	if !strings.Contains(src, `dr.Get("/diagnose/live", a.handleOSScopedDiagnoseLive)`) {
		t.Error("the re-check endpoint is not registered as a GET on the site console; the button " +
			"would 404 and the panel would be exactly as unrefreshable as before")
	}
	if strings.Contains(src, `CSRFTokenMiddleware).Get("/diagnose/live"`) {
		t.Error("a read was put behind a CSRF token")
	}
}
