// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_domain_serves_test.go — what a domain serves must be changeable.
//
// THE GAP. `domain.Registry.Update(ctx, id, siteType, mailEnabled)` shipped and
// no handler ever called it. Both values were therefore chosen once, on the
// "Add a domain" form, and frozen for the life of the domain — so an operator
// who wanted a website, a blog and mail on one domain had no way to say so
// unless they had guessed it at the moment they typed the hostname.
//
// The combination was always REPRESENTABLE (a business site at "/" with the
// blog at "/blog", plus mail). It was simply unreachable.
//
// These tests are mostly about the CARD'S CLAIMS. The mail switch is a
// provisioning-and-presentation flag: it decides whether mail.<host> joins the
// domain's certificate and whether the client sees a mail card. It is NOT read
// by the mail stack, which serves accounts by address — so switching it off
// does not stop delivery to a mailbox that already exists. An operator who
// believes otherwise will reach for it as a kill-switch at the worst possible
// moment, which makes the copy a correctness problem rather than a wording one.

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/domain"
)

func secondaryDomain() domain.Domain {
	return domain.Domain{
		ID: "dom-1", Host: "haru.example", SiteType: domain.SiteBlog,
		Status: domain.StatusActive,
	}
}

// THE test. Every catalogued site type is offered, and the one in force is the
// one selected — a picker that silently resets the current value on save is
// worse than no picker.
func TestEverySiteTypeIsOfferedAndTheCurrentOneIsSelected(t *testing.T) {
	d := secondaryDomain()
	d.SiteType = domain.SiteBusinessSubpath
	card := domainServesCard(d, true)

	for _, o := range siteTypeOptions {
		if !strings.Contains(card, `value="`+o.Value+`"`) {
			t.Errorf("the picker does not offer %q, so that configuration is unreachable from the "+
				"panel — which is the whole defect this closes", o.Value)
		}
	}
	if !strings.Contains(card, `value="`+domain.SiteBusinessSubpath+`" selected`) {
		t.Errorf("the domain's CURRENT type is not pre-selected, so saving any other field silently "+
			"changes what the domain serves:\n%s", card)
	}
}

// The combination the operator actually asked for must be selectable and
// described in terms a person can act on.
func TestWebsiteAndBlogOnOneDomainIsOfferedInPlainLanguage(t *testing.T) {
	card := domainServesCard(secondaryDomain(), true)
	if !strings.Contains(card, "website at the root, blog at /blog") {
		t.Errorf("the website+blog option is not described by what a visitor sees. "+
			"%q means nothing to an operator:\n%s", domain.SiteBusinessSubpath, card)
	}
	if !strings.Contains(card, "/blog") {
		t.Error("the hint does not name the path the blog will answer on")
	}
}

// The mail switch must reflect the stored state, both ways.
func TestTheMailSwitchReflectsTheStoredState(t *testing.T) {
	off := domainServesCard(secondaryDomain(), true)
	if strings.Contains(off, `id="serves-mail" checked`) {
		t.Error("mail reads as on for a domain that has it off")
	}
	d := secondaryDomain()
	d.MailEnabled = true
	on := domainServesCard(d, true)
	if !strings.Contains(on, `checked`) {
		t.Errorf("mail reads as off for a domain that has it on:\n%s", on)
	}
}

// THE claim test, and the one that matters most.
//
// Turning mail off is not a delivery kill-switch. internal/vayuos/mail never
// reads this flag — it serves accounts by address — so an existing mailbox
// keeps receiving. The card must say so rather than letting "off" imply
// "stopped".
func TestTheCardDoesNotImplyMailOffStopsDelivery(t *testing.T) {
	card := domainServesCard(secondaryDomain(), true)
	if !strings.Contains(card, "does <strong>not</strong> stop delivery") {
		t.Errorf("the card does not say that switching mail off leaves existing mailboxes "+
			"receiving. An operator reaching for this as a kill-switch would be wrong, and would "+
			"find out at the worst moment:\n%s", card)
	}
	if !strings.Contains(card, "VayuMail") {
		t.Error("the card does not say where an operator actually removes a mailbox")
	}
}

// Turning mail ON is not finished when the button goes green: the certificate
// needs mail.<host>, and only a provisioning run adds it.
func TestTheCardSaysTurningMailOnNeedsAProvisioningRun(t *testing.T) {
	d := secondaryDomain()
	card := domainServesCard(d, true)
	if !strings.Contains(card, "mail."+d.Host) {
		t.Errorf("the card does not name the certificate name that gets added:\n%s", card)
	}
	if !strings.Contains(card, "Provision now") {
		t.Error("the card does not point at the action that completes the change, so an operator " +
			"is left believing a saved setting is a working one")
	}
	if !strings.Contains(card, "not\n  instantly") && !strings.Contains(card, "not\ninstantly") &&
		!strings.Contains(card, "instantly") {
		t.Error("the card implies the certificate change is immediate")
	}
}

// An install with mail switched off globally is a DIFFERENT situation from a
// domain with mail off, and conflating them sends an operator to the wrong page.
func TestAnInstallWideMailSwitchOffIsDistinguished(t *testing.T) {
	withMail := domainServesCard(secondaryDomain(), true)
	if strings.Contains(withMail, "switched off for this whole install") {
		t.Error("an install with mail ON is being told mail is off install-wide")
	}
	noMail := domainServesCard(secondaryDomain(), false)
	if !strings.Contains(noMail, "switched off for this whole install") {
		t.Errorf("an install with mail off does not say so, so turning the domain switch on here "+
			"looks like it should work and silently will not:\n%s", noMail)
	}
	if !strings.Contains(noMail, "/os/vayumail") {
		t.Error("the note does not link to the page that actually turns mail on")
	}
}

// The primary domain's site type is the install's own site mode, set on
// Settings. Two controls on one value is how a panel starts disagreeing with
// itself.
func TestThePrimaryDomainIsNotEditableHere(t *testing.T) {
	d := secondaryDomain()
	d.IsPrimary = true
	card := domainServesCard(d, true)
	if strings.Contains(card, "serves-type") {
		t.Errorf("the primary domain got an editable picker; its site type is the install's own "+
			"site mode and a second control for it can only disagree with the first:\n%s", card)
	}
	if !strings.Contains(card, "/os/settings") {
		t.Error("the primary card does not point at the control that does own this value")
	}
}

// A hostile host must render as text.
func TestTheServesCardEscapesTheHost(t *testing.T) {
	d := secondaryDomain()
	d.Host = `x"><img onerror=alert(1) src=y>`
	card := domainServesCard(d, true)
	if strings.Contains(card, "<img onerror") {
		t.Errorf("the host reached the page as markup:\n%s", card)
	}
}

// House style and CSP.
func TestTheServesCardAndScriptAreCSPSafe(t *testing.T) {
	assertCSPSafe(t, "serves card", domainServesCard(secondaryDomain(), true))
	assertCSPSafe(t, "serves card (primary)", func() string {
		d := secondaryDomain()
		d.IsPrimary = true
		return domainServesCard(d, true)
	}())
}

// The id must travel as data, never spliced into JavaScript.
func TestTheDomainIDIsReadFromTheDOMNotSplicedIntoScript(t *testing.T) {
	script := domainServesScript("n0nce")
	if !strings.Contains(script, "getAttribute('data-id')") {
		t.Error("the script does not read the domain id from the DOM; interpolating a value into " +
			"JavaScript source is how a quote becomes a parse error that binds nothing")
	}
	card := domainServesCard(secondaryDomain(), true)
	if !strings.Contains(card, `data-id="dom-1"`) {
		t.Errorf("the card does not carry the domain id for the script to read:\n%s", card)
	}
}

// The script must parse. A parse error binds NOTHING, and this codebase has
// shipped a console where every button was inert for exactly that reason.
func TestTheServesScriptIsBalanced(t *testing.T) {
	s := domainServesScript("n0nce")
	if o, c := strings.Count(s, "{"), strings.Count(s, "}"); o != c {
		t.Errorf("brace mismatch in the serves script: %d open, %d close", o, c)
	}
	if o, c := strings.Count(s, "("), strings.Count(s, ")"); o != c {
		t.Errorf("paren mismatch in the serves script: %d open, %d close", o, c)
	}
	if !strings.Contains(s, `nonce="n0nce"`) {
		t.Error("the script does not carry the request nonce, so the CSP will refuse it")
	}
}

// knownSiteType must read the SAME catalogue the form renders. Two copies of a
// truth is how the theme exporter leaked a credential.
func TestTheValidatorReadsTheSameCatalogueTheFormRenders(t *testing.T) {
	for _, o := range siteTypeOptions {
		if !knownSiteType(o.Value) {
			t.Errorf("the form offers %q and the handler rejects it", o.Value)
		}
	}
	if knownSiteType("something-else") {
		t.Error("an unknown site type was accepted; the registry would store a value nothing renders")
	}
	if knownSiteType("") {
		t.Error("the empty string is accepted by the validator; the handler maps it to blog BEFORE " +
			"validating, and letting it through here would store an empty type")
	}
}

// ── Authorisation ────────────────────────────────────────────────────────────
//
// From the pre-release adversarial pass. This endpoint changes what a domain
// serves and whether it carries mail, so the question worth asking is who can
// reach it — a client login is scoped to exactly one site, and an API key may
// be scoped to a section that has nothing to do with domains.

// The route must require domains:write, not fall into the unmapped bucket and
// not inherit some weaker section. Unmapped is fail-closed (superuser only), so
// this is about the mapping being DELIBERATE rather than accidental.
func TestTheServesRouteRequiresTheDomainsCapability(t *testing.T) {
	sec, act, mapped := capabilityFor("POST", "/os/api/domains/dom-1/serves")
	if !mapped {
		t.Fatal("the serves route is unmapped, so only a superuser key can call it. Fail-closed is " +
			"safe, but an unmapped write route is an accident rather than a decision.")
	}
	if string(sec) != "domains" {
		t.Errorf("the serves route maps to section %q; changing what a domain serves is a domains "+
			"capability", sec)
	}
	if string(act) != "write" {
		t.Errorf("the serves route maps to action %q, want write", act)
	}
}

// A key holding some other section must not be able to flip a domain's mail on.
func TestAKeyWithoutDomainsWriteCannotChangeWhatADomainServes(t *testing.T) {
	sec, act, _ := capabilityFor("POST", "/os/api/domains/dom-1/serves")
	// A key granted posts:write and nothing else. External scope, so its stored
	// grants are the whole of its authority.
	perms := apikeys.NewPermissions()
	perms.Grant(apikeys.SectionPosts, apikeys.ActionWrite)
	postsOnly := apikeys.KeyInfo{ID: "k1", Scope: apikeys.ScopeExternal, Perms: perms}

	if postsOnly.Can(sec, act) {
		t.Error("a posts-scoped API key can change a domain's mail switch and site type")
	}
	if keyMayCall(postsOnly, "POST", "/os/api/domains/dom-1/serves") {
		t.Error("a posts-scoped API key is permitted to call the serves endpoint")
	}
	// And the correctly-scoped key still works — a guard that refuses everyone
	// has broken the feature rather than secured it.
	ok := apikeys.NewPermissions()
	ok.Grant(apikeys.SectionDomains, apikeys.ActionWrite)
	domainsKey := apikeys.KeyInfo{ID: "k2", Scope: apikeys.ScopeExternal, Perms: ok}
	if !keyMayCall(domainsKey, "POST", "/os/api/domains/dom-1/serves") {
		t.Error("a domains:write key is refused, so the endpoint is unreachable by design")
	}
}
