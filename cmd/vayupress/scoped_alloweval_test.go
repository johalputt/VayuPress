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

// The Website page in the house style — and, more importantly, a net under any
// future restyling of it.
//
// §11 opens every VayuOS page with the numbers that answer "what is the state of
// this?" before any control. This page had none, so an operator had to open
// sections to learn what the domain was even serving — which is exactly how a
// site sat on a stale uploaded bundle for a day with the file count visible only
// inside a collapsed radio hint.
//
// The ID assertions matter more than the tiles. Restyling a page is string
// surgery on markup that inline JavaScript addresses by id; drop one and a
// button goes quiet in the way this console has already proved it can.
func TestTheWebsitePageOpensWithItsStateAndKeepsEveryControl(t *testing.T) {
	d := siteWithEval(t, true)
	page := scopedWebsitePage(d, "bistro", bizsite.Content{Name: "Test"}, true,
		customsite.Manifest{Files: 30, HasPrev: true})

	for _, want := range []string{
		`class="stat-grid"`,
		"Serving at /", "Uploaded site", "Runtime code", "Certificate",
		"30 files", // the number that would have ended the stale-bundle hunt on sight
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not show %q, so its state cannot be read without opening things", want)
		}
	}

	// Every element the page's own script addresses. A restyle that loses one of
	// these leaves a control that looks present and does nothing.
	for _, id := range []string{
		"scoped-ctx", "scoped-bundle-file", "scoped-bundle-status", "scoped-bundle-outcome",
		"scoped-web-status", "scoped-web-template", "preview-path", "preview-status", "preview-out",
		"web-alloweval", "web-name", "web-tagline", "web-about", "web-showblog",
	} {
		if !strings.Contains(page, `id="`+id+`"`) {
			t.Errorf("id %q is gone from the page; the script that drives it will silently do nothing", id)
		}
	}
	for _, hook := range []string{"data-site-web-save", "data-bundle-upload", "data-site-preview"} {
		if !strings.Contains(page, hook) {
			t.Errorf("the %q control is missing", hook)
		}
	}
}

// The runtime-code tile must not report a relaxed policy as unremarkable, and
// must say nothing at all when there is no uploaded site for it to apply to.
func TestTheRuntimeCodeTileTellsTheTruthAboutThePolicy(t *testing.T) {
	onTile := statCardNamed(t, scopedWebsitePage(siteWithEval(t, true), "bistro",
		bizsite.Content{Name: "T"}, true, customsite.Manifest{Files: 3}), "Runtime code")
	if !strings.Contains(onTile, ">On<") {
		t.Errorf("a site running with eval permitted does not say so: %s", onTile)
	}
	if !strings.Contains(onTile, "stat-card--warn") {
		t.Errorf("a widened policy reads as ordinary: %s", onTile)
	}

	offTile := statCardNamed(t, scopedWebsitePage(siteWithEval(t, false), "bistro",
		bizsite.Content{Name: "T"}, true, customsite.Manifest{Files: 3}), "Runtime code")
	if !strings.Contains(offTile, ">Off<") {
		t.Errorf("a site with eval off does not say so: %s", offTile)
	}
	if strings.Contains(offTile, "stat-card--warn") {
		t.Errorf("a site with eval OFF is flagged as if the policy were widened: %s", offTile)
	}
}

// statCardNamed returns just the stat tile carrying the given label.
//
// Scoped deliberately, and for the second time in this codebase: an assertion
// that searches the WHOLE page for the warning class cannot tell which tile it
// found. Both the earlier mailbox test and the first version of this one passed
// against a mutation that reported a widened policy as ordinary, because the
// CERTIFICATE tile happened to carry the same class.
func statCardNamed(t *testing.T, page, label string) string {
	t.Helper()
	j := strings.Index(page, `>`+label+`</div>`)
	if j < 0 {
		t.Fatalf("there is no %q tile on the page", label)
	}
	// The OUTER tile, not the inner label div — both begin `<div class="stat-card`,
	// and matching the wrong one returns a slice with no class attribute in it,
	// so every assertion about the tone silently looks at nothing.
	i := -1
	for _, open := range []string{`<div class="stat-card"`, `<div class="stat-card `} {
		if k := strings.LastIndex(page[:j], open); k > i {
			i = k
		}
	}
	if i < 0 {
		t.Fatalf("the %q tile is malformed", label)
	}
	end := strings.Index(page[j:], `</div></div>`)
	if end < 0 {
		t.Fatalf("the %q tile is unterminated", label)
	}
	return page[i : j+end+len(`</div></div>`)]
}
