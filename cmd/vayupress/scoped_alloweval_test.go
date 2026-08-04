// SPDX-License-Identifier: Apache-2.0

package main

// scoped_alloweval_test.go — the eval opt-in, from the operator's side.
//
// Three defects, all found by trying to USE the setting rather than by reading
// the code that implements it:
//
//  1. Saving the Website page WIPED it. scopedWebsiteConfig builds a fresh
//     SiteConfig from mode + template + content and returned it; AllowEval is on
//     SiteConfig and was simply not carried. The connector restored it after the
//     call, so that path looked fine — the console did not, so an operator who
//     turned the setting on and later pressed "Save & publish" for an unrelated
//     reason lost every animation on their site, with nothing on screen changing
//     to say so.
//  2. There was NO CONTROL for it anywhere on the panel. It shipped in the
//     config and in the connector, and the only way to turn it on was to ask an
//     assistant to call a tool.
//  3. get_site did not report it, so it could be written and never read back.
//     update_site answered "published" whether or not the value had been stored,
//     and no second call could tell the difference.

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/customsite"
	"github.com/johalputt/vayupress/internal/domain"
)

func siteWithEval(t *testing.T, on bool) domain.Domain {
	t.Helper()
	raw, err := domain.EncodeSiteConfigInto("", domain.SiteConfig{
		Mode:      "business",
		Template:  "bistro",
		Content:   `{"name":"Test","tagline":"A line"}`,
		AllowEval: on,
	})
	if err != nil {
		t.Fatal(err)
	}
	return domain.Domain{ID: "d1", Host: "test.example", ConfigJSON: raw}
}

// An ordinary save — the operator edits a phone number and publishes. The eval
// opt-in is not on that form's mind at all, and it must come out the other side
// exactly as it went in.
func TestSavingTheWebsitePageDoesNotSilentlyTurnEvalOff(t *testing.T) {
	d := siteWithEval(t, true)
	if s, ok := d.Site(); !ok || !s.AllowEval {
		t.Fatal("fixture is wrong: the domain does not start with the opt-in on")
	}

	cfg, err := scopedWebsiteConfig(d, "business", "bistro", bizsite.Content{Name: "Test", Phone: "0123"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowEval {
		t.Fatal("the opt-in was turned off by a save that never mentioned it. Every animation on " +
			"the operator's site stops, and nothing on the page says why.")
	}
}

// And the converse, so the carry-forward cannot be "return true and pass".
func TestSavingDoesNotTurnEvalOnByItself(t *testing.T) {
	d := siteWithEval(t, false)
	cfg, err := scopedWebsiteConfig(d, "business", "bistro", bizsite.Content{Name: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AllowEval {
		t.Fatal("a save enabled eval on a site that had never asked for it — this widens the " +
			"policy on somebody's live domain without them choosing it")
	}
}

// The page has to offer the control, or the setting is only reachable by asking
// an assistant to call a tool — which is the thing this product exists not to do.
func TestTheWebsitePageOffersTheEvalControlOnceABundleIsDeployed(t *testing.T) {
	d := siteWithEval(t, true)
	c := bizsite.Content{Name: "Test"}

	withBundle := scopedWebsitePage(d, "bistro", c, true, customsite.Manifest{Files: 12, HasPrev: true})
	if !strings.Contains(withBundle, `id="web-alloweval"`) {
		t.Fatal("no control for the eval opt-in on the Website page, so the only way to set it is " +
			"through a tool call")
	}
	if !strings.Contains(withBundle, "checked") {
		t.Error("the control does not reflect the stored value, so it reads as off while the site " +
			"is actually running with it on")
	}
	// The copy must say what it does and what it costs, in the operator's terms.
	for _, phrase := range []string{"eval", d.Host} {
		if !strings.Contains(withBundle, phrase) {
			t.Errorf("the control never mentions %q, so the operator cannot tell what they are agreeing to", phrase)
		}
	}
	// Scope claim: it must not imply this affects only this site if that is untrue,
	// and it must state the scope, because a security setting with an unstated
	// blast radius is one nobody can consent to.
	if !strings.Contains(withBundle, "never to your panel") {
		t.Error("the control does not state its blast radius")
	}

	// With no bundle deployed the setting changes nothing, and a control that
	// does nothing is worse than an absent one.
	noBundle := scopedWebsitePage(d, "bistro", c, false, customsite.Manifest{Files: 12, HasPrev: true})
	if strings.Contains(noBundle, `id="web-alloweval"`) {
		t.Error("the control is offered on a template site, where it has no effect whatsoever")
	}
}

// Off must render as off. A checkbox that is always checked would tell every
// operator their site runs with a widened policy.
func TestTheEvalControlRendersUncheckedWhenTheSettingIsOff(t *testing.T) {
	d := siteWithEval(t, false)
	page := scopedWebsitePage(d, "bistro", bizsite.Content{Name: "Test"}, true, customsite.Manifest{Files: 12, HasPrev: true})
	i := strings.Index(page, `id="web-alloweval"`)
	if i < 0 {
		t.Fatal("the control is missing")
	}
	// Look only at the input element itself, not at the radio buttons above it.
	end := strings.Index(page[i:], ">")
	if end < 0 {
		t.Fatal("malformed control")
	}
	if strings.Contains(page[i:i+end], "checked") {
		t.Fatal("the control reads as ON for a site that has the setting OFF")
	}
}
