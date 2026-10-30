// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
)

// The band's figures are the readers' own: visitors, page views, visits, how
// many stopped at one page and how long the others stayed, each named once
// and linked to its definition. The server's count of requests, crawlers
// included, is not among them: labelled alike they read as a contradiction
// (an operator saw "31643 views" above a page-view figure of 1327), so it is
// said only where the server's measures are. Asserted on the figures the page
// draws, not on its source text.
func TestTheBandIsTheReadersFiguresEachLinkedToItsDefinition(t *testing.T) {
	figs := visitorFigures(&analytics.Overview{UniqueVisitors: 400, TotalPageviews: 1327, TotalVisits: 500, BounceRate: 40, AvgDuration: 75},
		&analytics.Overview{UniqueVisitors: 200, TotalPageviews: 1327, TotalVisits: 0})
	var got []string
	for _, f := range figs {
		got = append(got, f.Label+" = "+f.Value+" | "+f.Note+" | "+f.Def)
	}
	want := []string{
		"Visitors = 400 | Up 100% on the period before | /docs/ANALYTICS#visitor",
		"Page views = 1,327 | The same as the period before | /docs/ANALYTICS#page-view",
		// A period before with no visits has no change to give.
		"Visits = 500 |  | /docs/ANALYTICS#visit",
		"Bounce rate = 40% | Visits of one page and nothing else | /docs/ANALYTICS#bounce",
		"Average visit = 1m 15s | Over the visits of more than one page | /docs/ANALYTICS#average-visit",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("figures:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// No visitor is said once, in the page's state, not as a row of zeros;
	// visits and their length are left out until there are some.
	if figs := visitorFigures(&analytics.Overview{}, nil); len(figs) != 0 {
		t.Errorf("an install with no traffic shows %d figures", len(figs))
	}
	if figs := visitorFigures(&analytics.Overview{UniqueVisitors: 3, TotalPageviews: 5}, nil); len(figs) != 2 {
		t.Errorf("with no visits counted, %d figures; want visitors and page views", len(figs))
	}
}

// Every definition a figure links to is a heading of the guide it names, so
// the link opens on it rather than at the top of a long page.
func TestEachFiguresDefinitionIsAHeadingOfTheGuide(t *testing.T) {
	raw, err := fs.ReadFile(docsContentFS, "ANALYTICS.md")
	if err != nil {
		t.Fatal(err)
	}
	page := string(renderMarkdownDocument(raw))
	for _, f := range visitorFigures(&analytics.Overview{UniqueVisitors: 1, TotalPageviews: 1, TotalVisits: 1, AvgDuration: 1}, nil) {
		id, ok := strings.CutPrefix(f.Def, analyticsDefs)
		if !ok || !strings.Contains(page, `<h3 id="`+id+`">`) {
			t.Errorf("%s links to %q, which is no heading of docs/ANALYTICS.md", f.Label, f.Def)
		}
	}
}

// The shortest range is today so far, as every range is a set of UTC days,
// and it is said so: "24 hours" said what it was not. A page counted for a
// request and the same page counted by the warmer say the same words.
func TestTodayIsSaidAsTheDaySoFarInUTC(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/os/analytics?days=1", nil)
	if days, words := analyticsPeriod(r); days != 1 || words != "today, since midnight UTC" {
		t.Errorf("?days=1 = %d, %q", days, words)
	}
	if got, _ := osPeriodSelector("/os/analytics", "overview", analyticsSpan{days: 1}); !strings.Contains(string(got), `href="/os/analytics?days=1" aria-current="true">Today<`) {
		t.Errorf("the chooser does not name today: %s", got)
	}
	for days, want := range map[int]string{1: "today, since midnight UTC", 30: "in the last 30 days"} {
		if got := analyticsWhen(days); got != want {
			t.Errorf("analyticsWhen(%d) = %q, want %q", days, got, want)
		}
	}
}
