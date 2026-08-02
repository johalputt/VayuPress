// SPDX-License-Identifier: Apache-2.0

package render

import (
	"os"
	"strings"
	"testing"
)

// templateSource returns the raw backtick literal a named template is parsed
// from, read out of the shipped source file.
//
// Reading the file rather than rendering is what makes this checkable at all:
// the templates are package-level vars built at init, and the thing under test
// is a <script> tag, which is present or absent in the literal itself.
func templateSource(varName string) string {
	b, err := os.ReadFile("render.go")
	if err != nil {
		return ""
	}
	s := string(b)
	i := strings.Index(s, "var "+varName+" = ")
	if i < 0 {
		return ""
	}
	rest := s[i:]
	// Seek the literal that Parse() consumes, not merely the first backtick after
	// the var. articleTmpl declares a multi-line Funcs(template.FuncMap{...})
	// first, and taking the first backtick lands inside that map — which reads as
	// "this template has no beacon" for a template that does.
	if p := strings.Index(rest, ".Parse("); p >= 0 {
		rest = rest[p:]
	}
	open := strings.Index(rest, "`")
	if open < 0 {
		return ""
	}
	rest = rest[open+1:]
	end := strings.Index(rest, "`")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func notFoundTemplateSource() string { return templateSource("notFoundTmpl") }

// A page that does not exist is not a page view.
//
// The 404 template loaded the analytics beacon, and the beacon reports
// `location.pathname` — whatever URL the browser happens to be on. So every
// missing URL recorded itself as a pageview OF THAT PATH, and the pageview
// table filled with paths the site has never served.
//
// The consequence is not a slightly-high number, it is a number that means
// something different from what it says. On a site being probed, the reported
// pageview total is dominated by URLs that 404ed: the figure reads as audience
// and is mostly scanners. Anyone quoting it — in a report, on a pricing page, to
// an advertiser — is quoting the wrong thing, and the same rows put invented
// paths into "Top pages", where a resized-image variant and a CDN-internal path
// outranked real articles.
//
// Fixing it at the source rather than filtering at read time is deliberate. A
// reporting-side filter leaves the rows in the table for every OTHER consumer —
// trending, the public widget, the export — each of which would need the same
// filter, and one of them would be missed.
func TestA404DoesNotRecordItselfAsAPageview(t *testing.T) {
	src := notFoundTemplateSource()
	if src == "" {
		t.Fatal("could not read the 404 template source")
	}
	if strings.Contains(src, "vp-analytics.js") {
		t.Error("the 404 page still loads the analytics beacon. Every URL that 404s — every " +
			"scanner probe, every dead link, every missing image variant — is recorded as a " +
			"pageview of a path the site does not serve, and the reported total stops meaning " +
			"audience")
	}
}

// The beacon must still load on pages that DO exist, or the fix above has traded
// a wrong number for no number.
func TestRealPagesStillCarryTheBeacon(t *testing.T) {
	var carried int
	for name, src := range map[string]string{
		"article": templateSource("articleTmpl"),
		"home":    templateSource("homeTmpl"),
	} {
		if src == "" {
			continue
		}
		if !strings.Contains(src, "vp-analytics.js") {
			t.Errorf("the %s template no longer loads the beacon, so real traffic stops being "+
				"counted — that is a worse outcome than the bug being fixed", name)
			continue
		}
		carried++
	}
	if carried == 0 {
		t.Fatal("no page template was found to carry the beacon; this test proved nothing")
	}
}
