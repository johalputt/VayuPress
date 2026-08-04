// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/customsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
)

// Markup must not reference a class the stylesheet does not define.
//
// This gate exists because it happened. `scoped-tool`, `scoped-tool__icon` and
// `scoped-tool__body` were emitted for two releases with NO rule matching them.
// Every tile therefore collapsed to inline text — the title running straight
// into the description, underlined on hover by the default anchor style — while
// the page passed every test it had: the link was present, the markup was
// CSP-safe, the scope was correct. Nothing anywhere asserted it would look like
// anything.
//
// That is the visual half of a defect class this codebase already names on the
// behavioural side: right mechanism, wrong claim. A page that renders is not a
// page that works, and "it compiled and the test passed" has never been evidence
// about layout.

// classAttrRe pulls every class="…" value out of rendered markup.
var classAttrRe = regexp.MustCompile(`class="([^"]*)"`)

// cssIgnoredClasses are utility and state tokens defined elsewhere than
// admin-os.css (the shared admin stylesheet, Pico, or set by script), listed
// explicitly so the gate stays a real check rather than a permissive one.
var cssIgnoredClasses = map[string]bool{
	"mono": true, "muted": true, "hidden": true,
	"text-sm": true, "text-xs": true, "text-lg": true,
	"mb-6": true, "mt-4": true,
}

func loadAdminOSCSS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean("../../static/css/admin-os.css"))
	if err != nil {
		t.Skipf("admin-os.css not readable from here: %v", err)
	}
	return string(b)
}

// assertClassesAreStyled fails for any class in the markup with no rule in the
// stylesheet.
func assertClassesAreStyled(t *testing.T, label, css, markup string) {
	t.Helper()
	seen := map[string]bool{}
	var missing []string
	for _, m := range classAttrRe.FindAllStringSubmatch(markup, -1) {
		for _, cls := range strings.Fields(m[1]) {
			if cls == "" || seen[cls] || cssIgnoredClasses[cls] {
				continue
			}
			seen[cls] = true
			// A rule may be `.x {`, `.x:hover`, `.x::after`, `.x--mod` or `.a .x`,
			// so the selector token is matched rather than a whole rule — but it
			// must END where the class ends.
			//
			// A plain substring test says `.page-head` is styled because
			// `.page-header` exists. That is not a hypothetical: `page-head` and
			// `page-title` were emitted by seven pages with no rule anywhere in the
			// stylesheet the console actually loads, and this gate — written after
			// `scoped-tool` shipped unstyled for two releases — passed them the
			// whole time. The same defect the gate exists to catch, hidden inside
			// the gate.
			if !cssDefines(css, cls) {
				missing = append(missing, cls)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%s emits %d class(es) with NO rule in admin-os.css: %s\n"+
			"Every element carrying one renders unstyled — inline, run together, and "+
			"underlined on hover by the browser default. This is what shipped.",
			label, len(missing), strings.Join(missing, ", "))
	}
}

func TestTheSiteConsoleEmitsNoUnstyledClass(t *testing.T) {
	css := loadAdminOSCSS(t)
	d := isolationDomain()
	assertClassesAreStyled(t, "the site console", css, scopedConsolePage(d, 3, 2, 1, true, nil, nil, nil, nil))
}

func TestTheSiteToolTilesMatchTheAdministrationRows(t *testing.T) {
	css := loadAdminOSCSS(t)
	page := scopedConsolePage(isolationDomain(), 3, 2, 1, true, nil, nil, nil,
		map[string]scopedToolChip{"content": {On: true, Text: "3 items"}})

	// The PROPERTY, not the implementation. These rows and the administration
	// summaries are two bands on one page, one directly under the other, and they
	// read as one family only while they share the inner grammar. The first
	// version of this test pinned a second set of class names that happened to
	// carry matching rules — and the two drifted anyway: the tool rows ended up
	// with no chip, longer subtitles and their own title weight, which is what the
	// page was reported for. Asserting the rows USE the summary's classes is what
	// makes drift impossible rather than merely unlikely.
	band := betweenMarkers(t, page, "This site's tools", "Site administration")
	for _, cls := range []string{"mon-acc__ic", "mon-acc__head", "mon-acc__title", "mon-acc__sub"} {
		if !strings.Contains(band, `class="`+cls+`"`) {
			t.Errorf("the tool rows do not use %q, so they are a second component that has to be "+
				"kept in step with the accordions by hand", cls)
		}
	}

	// Every row reports its state while shut. This is the reported defect: four
	// administration rows carried a chip and six tool rows carried none, on the
	// same page, in the same stack.
	if chips := strings.Count(band, `class="mon-chip`); chips < len(scopedTools) {
		t.Errorf("%d tool rows but %d chips — a row that states nothing about its own state is "+
			"the break in the house style this page was reported for", len(scopedTools), chips)
	}

	for _, want := range []string{".scoped-tool", ".mon-acc__ic", ".mon-acc__head", ".mon-chip"} {
		if !strings.Contains(css, want) {
			t.Fatalf("%s has no rule at all, so the row renders as inline text", want)
		}
	}
	// The underline was the reported symptom. It must be removed explicitly —
	// inheriting whatever the anchor default happens to be is how it came back.
	i := strings.Index(css, "a.scoped-tool:hover")
	if i < 0 {
		t.Fatal("no hover rule for the tool tiles, so hovering underlines them")
	}
	if !strings.Contains(css[i:i+400], "text-decoration: none") {
		t.Error("the hover rule does not clear the underline, which is the reported symptom")
	}
	// A tile that is not a link must not carry a link's affordance.
	if !strings.Contains(css, ".scoped-tool--soon") {
		t.Error("an unscoped tool has no distinct style, so it looks clickable and is not")
	}
}

func TestTheOtherPerSitePagesEmitNoUnstyledClass(t *testing.T) {
	css := loadAdminOSCSS(t)
	d := isolationDomain()
	assertClassesAreStyled(t, "the content page", css, scopedContentPage(d, []dbpkg.Article{
		{Title: "One", Slug: "one", Status: "published"},
		{Title: "Two", Slug: "two", Status: "draft"},
	}))
	assertClassesAreStyled(t, "the website page", css, scopedWebsitePage(d, "studio", bizsite.Content{Name: "X"}, false, customsite.Manifest{}))
	assertClassesAreStyled(t, "the site list", css, domainsHeader([]domain.Domain{d}, ""))

	// The three that were never passed to this gate — which is the other half of
	// why an unstyled header survived on them. A gate covering four of seven pages
	// reports on four of seven pages, and reads like a clean bill of health for
	// all seven.
	assertClassesAreStyled(t, "the settings page", css,
		scopedSettingsBody("d1", "customer.example",
			map[string]string{settings.KeySiteName: "Customer Ltd"}, presCustom))
	assertClassesAreStyled(t, "the SEO page", css,
		scopedSEOBody("d1", "https://customer.example", map[string]string{}))
	assertClassesAreStyled(t, "the visitors page", css,
		scopedAnalyticsBody(1200, 340, 41.5, 62, []analytics.PageStat{{Path: "/", Pageviews: 900}}))
}

// betweenMarkers returns the markup between two landmarks, so an assertion about
// one band cannot be satisfied by an element in another. Whole-page searches for
// a class have passed against three separate mutations in this console already.
func betweenMarkers(t *testing.T, page, from, to string) string {
	t.Helper()
	i := strings.Index(page, from)
	if i < 0 {
		t.Fatalf("landmark %q is not on the page", from)
	}
	rest := page[i+len(from):]
	j := strings.Index(rest, to)
	if j < 0 {
		t.Fatalf("landmark %q does not follow %q", to, from)
	}
	return rest[:j]
}

// cssDefines reports whether the stylesheet carries a rule for exactly this
// class — `.cls` not followed by another identifier character, so `.page-header`
// does not answer for `.page-head`.
func cssDefines(css, cls string) bool {
	re := regexp.MustCompile(`\.` + regexp.QuoteMeta(cls) + `([^A-Za-z0-9_-]|$)`)
	return re.MatchString(css)
}

// The gate's own matcher, held to the case that defeated it.
func TestTheClassMatcherDoesNotAcceptAPrefixMatch(t *testing.T) {
	const css = `.page-header { display:flex } .stat-card--warn { color:red } .mon-chip{}`
	for _, defined := range []string{"page-header", "stat-card--warn", "mon-chip"} {
		if !cssDefines(css, defined) {
			t.Errorf("%q has a rule and the matcher says it does not", defined)
		}
	}
	for _, undefined := range []string{"page-head", "page", "stat-card", "mon", "mon-chi"} {
		if cssDefines(css, undefined) {
			t.Errorf("%q has NO rule, but a longer class name answered for it — this is exactly "+
				"how seven pages shipped an unstyled header past this gate", undefined)
		}
	}
}
