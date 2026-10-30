// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// A measured list is its count, as before, and carries on each row every
// measure of its visits for the switch, with the card setting each beside
// the page's own. Its rows come largest first by the list's own count, and
// the switch offers that count and the measures, never both counts.
func TestAMeasuredListCarriesWhatItsVisitsDid(t *testing.T) {
	rows := []measuredRow{
		{Label: "small.example", Href: "/os/analytics?f=x", M: analytics.VisitMeasure{Key: "small.example", Visits: 2, Visitors: 2, Views: 10, Long: 2, LongSecs: 300, Converted: 1}},
		{Label: "big.example", M: analytics.VisitMeasure{Key: "big.example", Visits: 8, Visitors: 7, Views: 8, Bounces: 8}},
	}
	ov := &analytics.Overview{TotalVisits: 10, TotalPageviews: 18, BounceRate: 80, AvgDuration: 150}
	out := osMeasuredList(rows, "visits", ov, "")
	if strings.Index(out, "big.example") > strings.Index(out, "small.example") {
		t.Error("the list is not largest first by its count")
	}
	for _, want := range []string{
		`data-measure="visits" aria-pressed="true">Visits</button>`,
		`data-v-pages="5" data-t-pages="5.0"`,      // ten views over two visits
		`data-v-bounce="100" data-t-bounce="100%"`, // big.example: every visit one page
		`data-t-length="2m 30s"`,                   // small.example: 300 s over its two visits
		`data-v-converted="50" data-t-converted="50%"`,
		`<span class="vp-bar__card-k">Bounce</span><span class="vp-bar__card-v">0%<span class="vp-bar__card-all">80% all</span></span>`,
		`<span class="vp-bar__val" data-measure-val>8<span class="vp-bar__pct">80%</span></span>`,
		`<a class="vp-bar__door" href="/os/analytics?f=x">small.example</a>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the list has no %q", want)
		}
	}
	if strings.Contains(out, `data-measure="visitors"`) {
		t.Error("a list of visits also offers visitors")
	}
	if strings.Contains(out, `<div class="sa-measure" role="group" aria-label="Rank by">`) {
		t.Error("the switch shows before its script can work it")
	}
}

// The Sources, Content and Audience views draw their visit lists measured:
// a referrer, a landing page and a device each carry their visits' bounce.
func TestTheVisitListsAreMeasured(t *testing.T) {
	a := focusApp(t)
	ctx := context.Background()
	for view, list := range map[string]string{"sources": "referrers", "content": "landing", "audience": "devices"} {
		page := a.renderSiteAnalyticsBody(ctx, primaryView, "example.test", 7, view)
		_, part, _ := strings.Cut(page, `data-part="`+list+`"`)
		part, _, _ = strings.Cut(part, `data-part="`)
		if !strings.Contains(part, "data-v-bounce=") {
			t.Errorf("%s's %s are not measured", view, list)
		}
	}
}

// A day a post was published is marked on the chart and named in its
// readout: the site's own posts, never another site's, nor a page or a
// draft.
func TestTheChartMarksTheDaysPostsWentOut(t *testing.T) {
	openMigratedDB(t)
	a := &App{analytics: analytics.New(dbpkg.DB)}
	day := time.Now().UTC().AddDate(0, 0, -1)
	for _, p := range []struct {
		id, title, site, status string
		page                    int
	}{
		{"a1", "Our post", "", "published", 0},
		{"a2", "Another site's post", "s9", "published", 0},
		{"a3", "A page", "", "published", 1},
		{"a4", "A draft", "", "draft", 0},
	} {
		if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,created_at,updated_at,domain_id,status,is_page) VALUES(?,?,?,'',?,?,?,?,?)`,
			p.id, p.title, p.id, day, day, p.site, p.status, p.page); err != nil {
			t.Fatal(err)
		}
	}
	got := a.postsByDay(context.Background(), primaryView, day.AddDate(0, 0, -1).Format("2006-01-02"), "9999-12-31")
	if len(got) != 1 || len(got[day.Format("2006-01-02")]) != 1 || got[day.Format("2006-01-02")][0] != "Our post" {
		t.Errorf("the primary's posts by day = %v, want Our post alone", got)
	}
	series := []analytics.DayPageviews{{Date: day.AddDate(0, 0, -1).Format("2006-01-02"), Count: 3, Visitors: 2}, {Date: day.Format("2006-01-02"), Count: 5, Visitors: 4}}
	chart := osTrendChart(series, "x", got)
	if n := len(regexp.MustCompile(`class="vp-trend-post"`).FindAllString(chart, -1)); n != 1 {
		t.Errorf("%d days marked, want the one", n)
	}
	if !strings.Contains(chart, `data-posts="Our post"`) || !strings.Contains(chart, "visitors. Published: Our post</title>") {
		t.Error("the day does not name its post")
	}
	if r, _ := postsSaid([]string{"a", "b", "c", "d", "e"}); r != "a · b · c and 2 more" {
		t.Errorf("five posts read %q", r)
	}
}
