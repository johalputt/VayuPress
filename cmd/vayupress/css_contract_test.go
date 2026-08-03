// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/bizsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
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
			// A rule may be `.x {`, `.x:hover`, `.x::after`, `.x--mod`, or
			// `.a .x`. Matching the bare selector token covers all of them.
			if !strings.Contains(css, "."+cls) {
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
	assertClassesAreStyled(t, "the site console", css, scopedConsolePage(d, 3, 2, 1, true, nil))
}

func TestTheSiteToolTilesMatchTheAdministrationRows(t *testing.T) {
	css := loadAdminOSCSS(t)
	// The tiles sit next to the administration accordions and must read as the
	// same family: same frame, same icon chip, same title/subtitle rhythm.
	for _, want := range []string{".scoped-tool", ".scoped-tool__icon", ".scoped-tool__body"} {
		if !strings.Contains(css, want) {
			t.Fatalf("%s has no rule at all, so the tile renders as inline text", want)
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
	assertClassesAreStyled(t, "the website page", css, scopedWebsitePage(d, "studio", bizsite.Content{Name: "X"}))
	assertClassesAreStyled(t, "the site list", css, domainsHeader([]domain.Domain{d}, ""))
}
