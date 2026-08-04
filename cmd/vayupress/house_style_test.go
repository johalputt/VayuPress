// SPDX-License-Identifier: Apache-2.0

package main

// house_style_test.go — one statement of the per-domain page contract, called by
// every page that claims to follow it.
//
// WHY ONE HELPER AND NOT FIVE ASSERTIONS. Converting five pages to the same
// style with five hand-written checks is how the five checks drift, and today
// already produced two that could not tell WHICH element they had found: an
// assertion searching a whole page for `stat-card--warn` passed against a
// mutation reporting a widened policy as ordinary, because the certificate tile
// carries that class too. A third counted `mon-chip` as a substring, which every
// chip contains twice, so a missing chip still cleared the threshold.
//
// So the rule lives here once. A page either satisfies it or does not.
//
// WHAT IT DOES NOT DO. It cannot tell you a page is well designed, only that it
// carries the structure §11 asks for. The per-page id-nets beside it are the
// part that actually catches the dangerous failure — a restyle that drops an id
// leaves a control that looks present and does nothing.

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/customsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// houseStyle describes what a page claims about itself, so the helper can hold
// it to that rather than to a blanket rule every page would have to satisfy.
type houseStyle struct {
	Name string // for failure messages: the page an operator would be looking at

	// MinTiles is how many stat-card tiles must open the page. §11 asks for the
	// numbers that answer "what is the state of this?" before any control.
	MinTiles int

	// MinBands is how many collapsible bands the page must have. 0 means the page
	// legitimately has none — a page with one card does not need a stack.
	MinBands int

	// IDs are every element the page's own inline script addresses. This is the
	// assertion that matters: markup can be restyled freely, and an id that goes
	// missing takes a control with it, silently.
	IDs []string

	// Hooks are data-* attributes the script binds click handlers to.
	Hooks []string
}

// assertHouseStyle holds one rendered page to its declared contract.
func assertHouseStyle(t *testing.T, page string, want houseStyle) {
	t.Helper()

	if want.MinTiles > 0 {
		if !strings.Contains(page, `class="stat-grid"`) {
			t.Errorf("%s: no stat-grid, so the page opens with controls before it says what state "+
				"the domain is in", want.Name)
		}
		// The OUTER tile only. `stat-card__label` and `stat-card__value` also begin
		// with `stat-card`, and counting those instead reports three tiles as nine.
		tiles := strings.Count(page, `<div class="stat-card"`) + strings.Count(page, `<div class="stat-card `)
		if tiles < want.MinTiles {
			t.Errorf("%s: %d stat tiles, want at least %d", want.Name, tiles, want.MinTiles)
		}
	}

	if want.MinBands > 0 {
		if !strings.Contains(page, `class="mon-stack"`) {
			t.Errorf("%s: the bands are not in a mon-stack, so the page is a flat scroll", want.Name)
		}
		opens := strings.Count(page, `<details class="mon-acc"`)
		closes := strings.Count(page, "</details>")
		if opens < want.MinBands {
			t.Errorf("%s: %d collapsible bands, want at least %d — every section should collapse",
				want.Name, opens, want.MinBands)
		}
		if opens != closes {
			t.Errorf("%s: %d <details> opened, %d closed — the page structure is broken",
				want.Name, opens, closes)
		}
		// Counted on the ATTRIBUTE. Every chip carries both `mon-chip` and
		// `mon-chip--on`/`--off`, so a bare substring count is double the truth and
		// a band that lost its chip still clears the threshold. That exact mutation
		// survived a first pass on the Website page.
		if chips := strings.Count(page, `class="mon-chip`); chips < opens {
			t.Errorf("%s: %d bands but %d chips — a collapsed band with no chip is a closed door "+
				"with no label", want.Name, opens, chips)
		}
	}

	// The failure that COMPILES. An unclosed div makes everything below it a child
	// of whatever was left open; the page still renders, just wrongly, and nothing
	// anywhere reports it.
	if d := strings.Count(page, "<div") - strings.Count(page, "</div>"); d != 0 {
		t.Errorf("%s: div tags unbalanced by %d — sections below the break become children of "+
			"something above them", want.Name, d)
	}

	for _, id := range want.IDs {
		if !strings.Contains(page, `id="`+id+`"`) {
			t.Errorf("%s: id %q is gone; the script that drives it will silently do nothing",
				want.Name, id)
		}
	}
	for _, h := range want.Hooks {
		if !strings.Contains(page, h) {
			t.Errorf("%s: the %q control is missing", want.Name, h)
		}
	}
}

// statCardIn returns just the stat tile carrying the given label, so an
// assertion about one tile's tone cannot be satisfied by a different tile.
func statCardIn(t *testing.T, page, label string) string {
	t.Helper()
	j := strings.Index(page, `>`+label+`</div>`)
	if j < 0 {
		t.Fatalf("there is no %q tile on the page", label)
	}
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

// The Website page is the worked example the other four are converted to match,
// so it is the helper's own first caller.
func TestWebsitePageMeetsTheHouseStyle(t *testing.T) {
	page := scopedWebsitePage(siteWithEval(t, true), "bistro",
		bizsiteContentForTest("Test"), true, customsiteManifestForTest(30))
	assertHouseStyle(t, page, houseStyle{
		Name:     "Website",
		MinTiles: 4,
		MinBands: 6,
		IDs: []string{
			"scoped-ctx", "scoped-bundle-file", "scoped-bundle-status", "scoped-bundle-outcome",
			"scoped-web-status", "scoped-web-template", "preview-path", "preview-status", "preview-out",
			"web-alloweval", "web-name", "web-tagline", "web-about", "web-showblog",
		},
		Hooks: []string{"data-site-web-save", "data-bundle-upload", "data-site-preview"},
	})
}

func bizsiteContentForTest(name string) bizsite.Content { return bizsite.Content{Name: name} }
func customsiteManifestForTest(files int) customsite.Manifest {
	return customsite.Manifest{Files: files, HasPrev: true}
}

// Phase 1 — Visitors. The smallest per-domain page, and the one that proves the
// helper works on something it was not written against.
func TestAnalyticsPageMeetsTheHouseStyle(t *testing.T) {
	withPages := scopedAnalyticsBody(1200, 340, 41.5, 62, []analytics.PageStat{
		{Path: "/", Pageviews: 900, UniqueVisitors: 260},
		{Path: "/about", Pageviews: 300, UniqueVisitors: 80},
	})
	assertHouseStyle(t, withPages, houseStyle{Name: "Visitors", MinTiles: 4, MinBands: 2})

	// The numbers must be the ones passed in, not a shape that happens to render.
	for _, want := range []string{"1200", "340", "42%", "62s", "/about"} {
		if !strings.Contains(withPages, want) {
			t.Errorf("the page does not show %q", want)
		}
	}

	// An empty site must still be a valid page, and must say why it is empty
	// rather than showing a bare zero.
	empty := scopedAnalyticsBody(0, 0, 0, 0, nil)
	assertHouseStyle(t, empty, houseStyle{Name: "Visitors (no traffic)", MinTiles: 4, MinBands: 2})
	if !strings.Contains(empty, "No visits recorded") {
		t.Error("a site with no traffic shows no explanation, only zeroes")
	}
	if !strings.Contains(empty, "nothing yet") {
		t.Error("the collapsed band does not say it is empty, so it reads as a closed door")
	}
}

// Phase 2 — the domain Home page. Its six bands were already collapsible; the
// deviation was the tiles, which used a third markup idiom.
func TestScopedHomePageMeetsTheHouseStyle(t *testing.T) {
	d := domainWithAllowance(t, true, 0)
	page := scopedConsolePage(d, 12, 3, 0, true, nil, nil, nil)
	// Five, not six: one band renders only for an install with the mail product
	// switched on, and a test that demanded six would be asserting a fixture
	// rather than the page.
	assertHouseStyle(t, page, houseStyle{Name: "Domain home", MinTiles: 4, MinBands: 5})

	// Labels are HTML-escaped on the way out — "Posts & pages" renders as
	// "Posts &amp; pages", and searching for the raw ampersand finds nothing. The
	// first version of this assertion did exactly that and reported a tile that
	// was plainly there as missing.
	for _, want := range []string{"Posts &amp; pages", "Members", "Mailboxes", "Certificate"} {
		if !strings.Contains(page, ">"+want+"</div>") {
			t.Errorf("no %q tile", want)
		}
	}
	if v := statCardIn(t, page, "Members"); !strings.Contains(v, ">3<") {
		t.Errorf("the members tile does not show the count it was given: %s", v)
	}
}

// Phase 3 — SEO. Built inline in its handler until now, so it had no test.
func TestScopedSEOPageMeetsTheHouseStyle(t *testing.T) {
	// A site that has set nothing: the state every new customer domain starts in.
	none := scopedSEOBody("d1", "https://customer.example", map[string]string{})
	assertHouseStyle(t, none, houseStyle{Name: "SEO (nothing set)", MinTiles: 4, MinBands: 3})
	if tile := statCardIn(t, none, "Directives set"); !strings.Contains(tile, "stat-card--warn") {
		t.Errorf("a site declaring nothing to crawlers is not flagged: %s", tile)
	}
	if !strings.Contains(none, "all default") {
		t.Error("the collapsed band does not say everything is still the product default")
	}

	// And one that has set something: the warning must clear, or it cries wolf.
	some := scopedSEOBody("d1", "https://customer.example", map[string]string{
		scopedSEOFields[0].Key: "A real title",
	})
	if tile := statCardIn(t, some, "Directives set"); strings.Contains(tile, "stat-card--warn") {
		t.Errorf("a site that has set a directive is still flagged: %s", tile)
	}
	if !strings.Contains(some, "A real title") {
		t.Error("the declared value is not shown, so the page cannot be checked against reality")
	}

	// An onion is served over http BY DESIGN. Reporting that as a fault would be
	// a claim defect: the page would be telling an operator to fix something
	// that is correct.
	onion := scopedSEOBody("d1", "http://abc.onion", map[string]string{})
	if tile := statCardIn(t, onion, "Scheme"); !strings.Contains(tile, ">http<") {
		t.Errorf("the scheme tile does not report what the origin actually uses: %s", tile)
	}
	if strings.Contains(statCardIn(t, onion, "Scheme"), "stat-card--warn") {
		t.Error("an onion served over http is toned as a problem; it is how onions work")
	}
}

// Phase 4 — Content. Its controls are addressed by id and by data-attribute, so
// the id-net matters more here than the structure does.
func TestScopedContentPageMeetsTheHouseStyle(t *testing.T) {
	d := domainWithAllowance(t, true, 2)

	withItems := scopedContentPage(d, []dbpkg.Article{
		{Title: "Hello", Slug: "hello", Status: "published", UpdatedAt: time.Now()},
		{Title: "Draft one", Slug: "draft-one", Status: "draft", UpdatedAt: time.Now()},
	})
	assertHouseStyle(t, withItems, houseStyle{
		Name: "Content", MinTiles: 4, MinBands: 2,
		IDs:   []string{"scoped-assign-slug"},
		Hooks: []string{"data-scoped-assign", "data-scoped-release"},
	})
	if !strings.Contains(withItems, "2 items") {
		t.Error("the collapsed band does not say how much this site owns")
	}
	if tile := statCardIn(t, withItems, "Drafts"); !strings.Contains(tile, ">1<") {
		t.Errorf("the drafts tile does not count the draft it was given: %s", tile)
	}

	// An empty site is the state a new customer domain starts in: it must still
	// be a valid page, and must explain itself rather than show four zeroes.
	empty := scopedContentPage(d, nil)
	assertHouseStyle(t, empty, houseStyle{
		Name: "Content (empty)", MinTiles: 4, MinBands: 2,
		IDs: []string{"scoped-assign-slug"}, Hooks: []string{"data-scoped-assign"},
	})
	if !strings.Contains(empty, "Nothing is published on this site yet") {
		t.Error("an empty site shows zeroes with no explanation")
	}
	if !strings.Contains(empty, "nothing yet") {
		t.Error("the collapsed band reads as a closed door with no label")
	}
}
