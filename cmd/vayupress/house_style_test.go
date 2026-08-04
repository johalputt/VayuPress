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
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/customsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
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
	page := scopedConsolePage(d, 12, 3, 0, true, nil, nil, nil, nil)
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

// Phase 5 — Settings. The only page in the set whose bands did not exist to be
// converted. It had no tiles, no section-heads and no accordions, so its
// structure had to be chosen rather than moved, and it was left until last so
// the other four could establish the pattern first.
func TestScopedSettingsPageMeetsTheHouseStyle(t *testing.T) {
	// The state a customer's domain is in on the day it is handed over.
	empty := scopedSettingsBody("d1", "customer.example", map[string]string{}, presDefault)
	assertHouseStyle(t, empty, houseStyle{
		Name: "Settings (nothing set)", MinTiles: 4, MinBands: 2,
		IDs:   []string{"scoped-ctx", "scoped-status", "scoped-style-status"},
		Hooks: []string{"data-scoped-save", "data-scoped-copy-style", "data-scoped-key"},
	})
	if tile := statCardIn(t, empty, "Identity"); !strings.Contains(tile, "stat-card--warn") {
		t.Errorf("a site with no name, tagline, description or by-line is not flagged: %s", tile)
	}
	descTile := statCardIn(t, empty, "Search description")
	if !strings.Contains(descTile, ">Missing<") {
		t.Errorf("a site with no meta description does not say so, and that is the one gap "+
			"with a cost: the engine writes its own sentence instead. %s", descTile)
	}
	// The word and the tone are separate mutations. Reporting "Missing" in the
	// unremarkable tone survived a pass that only checked the word.
	if !strings.Contains(descTile, "stat-card--warn") {
		t.Errorf("the missing description reads as ordinary state, so nothing on the page draws "+
			"an eye to it: %s", descTile)
	}
	// Each tile pinned in BOTH states. Asserting only the configured case lets a
	// tile that returns a constant pass — three of these four would survive a
	// hardcoded value if the empty page went unchecked.
	if tile := statCardIn(t, empty, "Identity"); !strings.Contains(tile, ">0 of 4<") {
		t.Errorf("the identity tile does not count zero as zero: %s", tile)
	}
	if tile := statCardIn(t, empty, "Author by-line"); !strings.Contains(tile, ">Default<") {
		t.Errorf("a site with no author claims one is set: %s", tile)
	}
	if tile := statCardIn(t, empty, "Presentation"); !strings.Contains(tile, ">Default<") {
		t.Errorf("a site still on the product theme is reported as carrying its own: %s", tile)
	}

	// A domain that has been filled in. Every warning must clear, or the page
	// cries wolf and an operator stops reading the tones at all.
	full := scopedSettingsBody("d1", "customer.example", map[string]string{
		settings.KeySiteName:        "Customer Ltd",
		settings.KeySiteTagline:     "We do the thing",
		settings.KeySiteDescription: "A sentence for search engines.",
		settings.KeySiteAuthor:      "A Person",
	}, presCustom)
	assertHouseStyle(t, full, houseStyle{Name: "Settings (filled in)", MinTiles: 4, MinBands: 2})
	for _, label := range []string{"Identity", "Search description"} {
		if v := statCardIn(t, full, label); strings.Contains(v, "stat-card--warn") {
			t.Errorf("the %q tile is still flagged on a fully configured site: %s", label, v)
		}
	}
	if v := statCardIn(t, full, "Identity"); !strings.Contains(v, ">4 of 4<") {
		t.Errorf("the identity tile miscounts what is set: %s", v)
	}
	if v := statCardIn(t, full, "Presentation"); !strings.Contains(v, ">Custom<") {
		t.Errorf("a domain carrying its own theme is reported as running the product default: %s", v)
	}
	if v := statCardIn(t, full, "Author by-line"); !strings.Contains(v, ">Set<") {
		t.Errorf("the by-line tile does not reflect the stored author: %s", v)
	}

	// The stored values must reach the inputs. A form that renders blanks over a
	// configured site saves those blanks the moment anyone presses Save.
	if !strings.Contains(full, `value="Customer Ltd"`) {
		t.Error("the stored site name never reaches its input, so opening the page and saving " +
			"would erase it")
	}
}

// Reachability, as its own statement. The copy-from-primary endpoint was routed
// and unit-tested from the day it was written, and no page anywhere offered it:
// the only way to run it was to craft a POST by hand. That is the eval opt-in's
// defect exactly, and a capability an operator cannot find is one the product
// does not have.
func TestTheCopyFromPrimaryActionIsReachableFromThePage(t *testing.T) {
	page := scopedSettingsBody("d1", "customer.example", map[string]string{}, presDefault)
	if !strings.Contains(page, "data-scoped-copy-style") {
		t.Fatal("no control for copy-from-primary on any page; the endpoint is reachable only by " +
			"hand-crafting a request")
	}
	script := scopedSettingsScript("n0nce")
	if !strings.Contains(script, "data-scoped-copy-style") {
		t.Fatal("the control is rendered but the script never looks for it, so pressing it does " +
			"nothing at all")
	}
	// Naming the selector is not binding it. Guarding the listener behind a
	// falsy condition leaves the selector in the source and the button dead, and
	// that mutation survived a pass that only searched for the attribute — so
	// pin the live binding itself.
	if !strings.Contains(script, "if(cpy)cpy.addEventListener('click',") {
		t.Error("the copy button is looked up but no click listener is attached to it; the control " +
			"renders, enables and does nothing when pressed")
	}
	if !strings.Contains(script, "/api/copy-from-primary") {
		t.Error("the handler is bound to no endpoint")
	}
	// The save button going missing must not take the copy button with it. The
	// original script returned early when [data-scoped-save] was absent.
	if strings.Contains(script, "if(!btn)return;") {
		t.Error("the copy handler is chained behind an early return on the save button, so one " +
			"missing control silently disables the other")
	}
}

// The band promises identity is not copied. That promise is worth exactly what
// the copy list says, so the two are pinned together: an edit that adds the site
// name to the copy list fails here rather than on a customer's live domain.
func TestTheHouseStyleBandDoesNotPromiseMoreThanTheCopyListDelivers(t *testing.T) {
	page := scopedSettingsBody("d1", "customer.example", map[string]string{}, presDefault)
	if !strings.Contains(page, "not</b> copied") {
		t.Fatal("the band no longer tells the operator that identity is excluded")
	}
	for _, identity := range []string{
		settings.KeySiteName, settings.KeySiteTagline,
		settings.KeySiteDescription, settings.KeySiteAuthor,
	} {
		for _, k := range copyableFromPrimary {
			if k == identity {
				t.Errorf("the page tells the operator %q is not copied onto a client's site, and it is",
					identity)
			}
		}
	}
	// And the converse: the band names what it does copy, so the description
	// cannot quietly drift away from the list.
	if len(copyableFromPrimary) == 0 {
		t.Fatal("the copy list is empty while the page advertises the action")
	}
}

// ─── Phase 6: the adversarial pass over the conversion ──────────────────────
//
// Started from "what would I do to this if I wanted it to fail", not from the
// list of what changed. Three things came out of it.

// FINDING 1 — the presentation tile stated a fact about a read that failed.
//
// The handler collapsed a settings-read error into `false`, which the page then
// rendered as "Default". An operator looking at a site with a custom theme, at
// the moment the settings store was unreachable, would have been told in plain
// words that it was on the product default. The error path is exactly where a
// confident wrong answer does the most damage, because nothing else on screen
// is contradicting it.
func TestThePresentationTileDoesNotInventAnAnswerWhenTheReadFailed(t *testing.T) {
	unknown := scopedSettingsBody("d1", "customer.example", map[string]string{}, presUnknown)
	tile := statCardIn(t, unknown, "Presentation")
	if strings.Contains(tile, ">Default<") || strings.Contains(tile, ">Custom<") {
		t.Errorf("a settings read that failed is reported as a definite state: %s", tile)
	}
	if !strings.Contains(tile, ">—<") {
		t.Errorf("the tile does not mark the value as unavailable: %s", tile)
	}
	// The chip must not claim it either — it is the half an operator reads while
	// the band is shut, so a confident chip over an honest tile is still a lie.
	if strings.Contains(unknown, `mon-chip--off">product default`) {
		t.Error("the collapsed band claims the product default for a state nobody could read")
	}
	if !strings.Contains(unknown, "not known") {
		t.Error("the band's chip does not say the state is unknown")
	}
	// And the honest states must still be distinguishable, or "unknown" has just
	// been made the answer to everything.
	if !strings.Contains(statCardIn(t, scopedSettingsBody("d", "h", nil, presDefault), "Presentation"), ">Default<") {
		t.Error("a site genuinely on the product default no longer says so")
	}
	if !strings.Contains(statCardIn(t, scopedSettingsBody("d", "h", nil, presCustom), "Presentation"), ">Custom<") {
		t.Error("a site genuinely carrying its own presentation no longer says so")
	}
}

// FINDING 2 — four of the five converted pages had no CSP assertion.
//
// Only the domain home page was covered. The house-style rule names
// assertCSPSafe as the thing standing between a page and an inline `style="…"`
// attribute, and a restyling is precisely the edit that introduces one. Every
// converted page is held to it now, not just the one that happened to have a
// test already.
func TestEveryConvertedPageIsCSPSafe(t *testing.T) {
	d := domainWithAllowance(t, true, 2)
	for _, c := range []struct {
		name string
		page string
	}{
		{"Website", scopedWebsitePage(siteWithEval(t, true), "bistro",
			bizsiteContentForTest("Test"), true, customsiteManifestForTest(30))},
		{"Visitors", scopedAnalyticsBody(1200, 340, 41.5, 62, []analytics.PageStat{
			{Path: "/", Pageviews: 900, UniqueVisitors: 260}})},
		{"Domain home", scopedConsolePage(d, 12, 3, 0, true, nil, nil, nil, nil)},
		{"SEO", scopedSEOBody("d1", "https://customer.example", map[string]string{})},
		{"Content", scopedContentPage(d, []dbpkg.Article{
			{Title: "Hello", Slug: "hello", Status: "published", UpdatedAt: time.Now()}})},
		{"Settings", scopedSettingsBody("d1", "customer.example",
			map[string]string{settings.KeySiteName: "Customer Ltd"}, presCustom)},
	} {
		assertCSPSafe(t, c.name, c.page)
	}
}

// FINDING 3 — the escaping was correct and nothing held it there.
//
// Every value a customer controls did reach the panel escaped, which is a real
// result rather than a skipped step. But no test said so, so swapping one
// `esc(…)` for a bare value would have passed every gate in the repository and
// put stored script into the operator's own console — an admin page is the worst
// possible place to land it, because the reader is the person with every
// permission.
//
// These pages are string concatenation, not templates, so the escaping is a
// convention rather than a property of the renderer. Conventions need tests.
func TestHostileCustomerContentReachesThePanelEscaped(t *testing.T) {
	const payload = `"><img src=x onerror=alert(1)>`

	d := domainWithAllowance(t, true, 2)
	for _, c := range []struct {
		name  string
		page  string
		field string
	}{
		{"the site name an operator typed into Settings",
			scopedSettingsBody("d1", "customer.example",
				map[string]string{settings.KeySiteName: payload}, presDefault), "site name"},
		{"a page path recorded by the analytics collector",
			scopedAnalyticsBody(10, 5, 0, 0, []analytics.PageStat{{Path: payload, Pageviews: 3}}), "path"},
		{"an article title",
			scopedContentPage(d, []dbpkg.Article{
				{Title: payload, Slug: "x", Status: "published", UpdatedAt: time.Now()}}), "title"},
		{"an SEO directive declared for the domain",
			scopedSEOBody("d1", "https://customer.example",
				map[string]string{scopedSEOFields[0].Key: payload}), "directive"},
	} {
		if strings.Contains(c.page, payload) {
			t.Errorf("%s reaches the panel unescaped — the %s is stored script running in the "+
				"console of the one reader who holds every permission", c.name, c.field)
		}
		// The value must still be VISIBLE, escaped. Dropping it entirely would
		// pass the check above while hiding what a site actually contains.
		if !strings.Contains(c.page, "&lt;img src=x") {
			t.Errorf("%s: the value is not rendered at all, so the page hides what the site "+
				"actually holds", c.name)
		}
	}
}

// A navigation row whose state could not be read says so.
//
// The Settings page shipped the opposite of this — a failed settings read
// rendered as "product default", stated as fact — and the same shape is
// available on every one of these six rows the moment a store is down. The
// cheerful branch is always the tempting one, because it is the branch you get
// for free from a zero value.
func TestAToolRowWithNoReadableStateSaysSoRatherThanGuessing(t *testing.T) {
	page := scopedConsolePage(isolationDomain(), 0, 0, 0, true, nil, nil, nil,
		map[string]scopedToolChip{}) // every store unreachable
	band := betweenMarkers(t, page, "This site's tools", "Site administration")

	// Read the CHIPS, not the band. Searching the whole band for a word finds it
	// in a subtitle — "Serve this domain as a blog or as a website" contains
	// "blog" — and an assertion that cannot tell which element it matched is the
	// mistake this file already documents three times. It caught this one too.
	chips := chipTextsIn(band)
	if len(chips) != len(scopedTools) {
		t.Fatalf("%d chips for %d rows", len(chips), len(scopedTools))
	}
	for i, got := range chips {
		if got != "—" {
			t.Errorf("row %d reports %q for a store nobody could read; the em dash is the only "+
				"honest answer there", i, got)
		}
	}
}

// chipTextsIn returns the text of every mon-chip in a fragment, in order.
var chipTextRe = regexp.MustCompile(`class="mon-chip[^"]*">([^<]*)<`)

func chipTextsIn(fragment string) []string {
	var out []string
	for _, m := range chipTextRe.FindAllStringSubmatch(fragment, -1) {
		out = append(out, m[1])
	}
	return out
}

// toolRowIn returns the one navigation row carrying the given title, so an
// assertion about that row's chip cannot be satisfied by another row's.
func toolRowIn(t *testing.T, band, title string) string {
	t.Helper()
	j := strings.Index(band, `>`+title+`</span>`)
	if j < 0 {
		t.Fatalf("there is no %q row in this band", title)
	}
	i := strings.LastIndex(band[:j], `class="scoped-tool`)
	if i < 0 {
		t.Fatalf("the %q row is malformed", title)
	}
	end := strings.Index(band[j:], `</a>`)
	if d := strings.Index(band[j:], `</div>`); d >= 0 && (end < 0 || d < end) {
		end = d
	}
	if end < 0 {
		t.Fatalf("the %q row is unterminated", title)
	}
	return band[i : j+end]
}

// The chip's TONE, not just its text.
//
// Forcing every chip to the "on" style survived a first mutation pass: a site
// with no name, no tagline and no description would have shown "nothing set" in
// the green that means good news, on the same page where four administration
// rows use that green honestly. A chip whose colour contradicts its words is
// worse than no chip, because the colour is what gets read at a glance.
func TestAToolRowsChipToneMatchesItsState(t *testing.T) {
	page := scopedConsolePage(isolationDomain(), 0, 0, 0, true, nil, nil, nil,
		map[string]scopedToolChip{
			"content":  {On: true, Text: "12 items"},
			"settings": {Text: "nothing set"},
		})
	band := betweenMarkers(t, page, "This site's tools", "Site administration")

	on := toolRowIn(t, band, "Posts &amp; pages")
	if !strings.Contains(on, "mon-chip--on") {
		t.Errorf("a site with twelve items reads as empty: %s", on)
	}
	// Tone and text are separate mutations. Dropping the words while keeping the
	// colour survived a pass that checked only the tone, and an empty pill tells
	// an operator nothing at all.
	if !strings.Contains(on, ">12 items<") {
		t.Errorf("the row does not show the count it was given: %s", on)
	}
	off := toolRowIn(t, band, "Site settings")
	if !strings.Contains(off, "mon-chip--off") {
		t.Errorf("a site with nothing set is chipped in the colour that means it is fine: %s", off)
	}
	if !strings.Contains(off, ">nothing set<") {
		t.Errorf("the row renders an empty pill instead of its state: %s", off)
	}
	if strings.Contains(off, "mon-chip--on") {
		t.Errorf("the empty state carries the positive tone: %s", off)
	}
}
