// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// A page's figures, collected as readers made them: its views, its visits
// and those with a custom event, the views that came from a search engine,
// the views of the days before, and another site's views not among them.
func TestEachPagesFiguresAreItsOwn(t *testing.T) {
	s := newExtStore(t)
	// An install has its own host, which makes the channel expression bind
	// its patterns: the search count must read its arguments in their place.
	prevDomain := config.Cfg.Domain
	config.Cfg.Domain = "site.test"
	t.Cleanup(func() { config.Cfg.Domain = prevDomain })
	ctx := context.Background()
	now := time.Now().UTC()
	hit := func(at time.Time, ip, site, path, ref string, kind int) {
		t.Helper()
		s.now = func() time.Time { return at }
		req := CollectRequest{URL: path, Referrer: ref, Hostname: "site.test", EventType: kind}
		if kind == 2 {
			req.EventName = "signup"
		}
		if err := s.Collect(ctx, req, ip, "Mozilla/5.0 Firefox/131.0", site); err != nil {
			t.Fatal(err)
		}
	}
	// Two visits to /a: one from a search engine that signs up, one direct.
	hit(now, "198.51.100.1", "s1", "/a", "https://www.google.com/", 1)
	hit(now.Add(time.Second), "198.51.100.1", "s1", "/a", "", 2)
	hit(now, "198.51.100.2", "s1", "/a", "", 1)
	hit(now.Add(time.Second), "198.51.100.2", "s1", "/b", "", 1)
	// Another site's /a, and /a's own past: 20 views a week before.
	hit(now, "198.51.100.3", "s2", "/a", "https://www.google.com/", 1)
	for i := 0; i < 20; i++ {
		hit(now.AddDate(0, 0, -10).Add(time.Duration(i)*time.Minute), fmt.Sprintf("198.51.100.%d", 10+i), "s1", "/gone", "", 1)
	}
	s.now = func() time.Time { return now }
	got, err := s.ContentPagesScoped(ctx, "s1", 7)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ContentPage{}
	for _, p := range got {
		by[p.Path] = p
	}
	if want := (ContentPage{Path: "/a", Views: 2, Visits: 2, Converted: 1, FromSearch: 1}); by["/a"] != want {
		t.Errorf("/a = %+v\nwant %+v", by["/a"], want)
	}
	if want := (ContentPage{Path: "/b", Views: 1, Visits: 1}); by["/b"] != want {
		t.Errorf("/b = %+v, want %+v", by["/b"], want)
	}
	if want := (ContentPage{Path: "/gone", PrevViews: 20}); by["/gone"] != want {
		t.Errorf("/gone = %+v, want its 20 views before and none now", by["/gone"])
	}
	if len(got) != 3 {
		t.Errorf("pages = %+v, want /a, /b and /gone", got)
	}
	all, _ := s.ContentPagesScoped(ctx, ScopeAllDomains, 7)
	for _, p := range all {
		if p.Path == "/a" && (p.Views != 3 || p.FromSearch != 2) {
			t.Errorf("every site's /a = %+v, want 3 views, 2 from search", p)
		}
	}
}

// eight pages compared for engagement, views 100..800 and engaged shares
// 10%..80% in another order, with the pages under test appended.
func compared(extra ...ContentPage) []ContentPage {
	var out []ContentPage
	for i := 1; i <= 8; i++ {
		out = append(out, ContentPage{Path: fmt.Sprintf("/p%d", i), Views: int64(100 * i), Readings: 100, Engaged: int64(10 * ((i+4)%8 + 1))})
	}
	return append(out, extra...)
}

func category(rep ContentReport, id string) map[string]string {
	out := map[string]string{}
	for _, c := range rep.Categories {
		if c.ID == id {
			for _, p := range c.Pages {
				out[p.Path] = p.Reason
			}
		}
	}
	return out
}

// Each rule, with a seed that meets it and one that just misses it.
func TestEachCategoryIsItsRule(t *testing.T) {
	// Of the 12 pages compared for engagement, the views' quartiles are 100,
	// 400 and 700 and the engaged shares' 10%, 40% and 70%: each page under
	// test sits on its thresholds, which it meets.
	rep := ClassifyContent(compared(
		ContentPage{Path: "/busy-light", Views: 700, Readings: 100, Engaged: 10},
		ContentPage{Path: "/busy-held", Views: 700, Readings: 100, Engaged: 70},
		ContentPage{Path: "/quiet-held", Views: 100, Readings: 100, Engaged: 70},
		ContentPage{Path: "/quiet-light", Views: 100, Readings: 100, Engaged: 10},
		ContentPage{Path: "/busy-unread", Views: 900, Readings: 9, Engaged: 0},
	))
	if c := category(rep, "many_views_little_engagement"); len(c) != 1 ||
		c["/busy-light"] != "700 views, in the top quarter of the 12 pages compared (700 views or more); 10% of 100 readings engaged, in the bottom quarter (10% or less)" {
		t.Errorf("many views, little engagement = %v, want /busy-light alone, said with its thresholds", c)
	}
	if c := category(rep, "many_views_much_engagement"); len(c) != 1 || c["/busy-held"] == "" {
		t.Errorf("many views, much engagement = %v, want /busy-held alone", c)
	}
	if c := category(rep, "few_views_much_engagement"); len(c) != 1 ||
		c["/quiet-held"] != "100 views, in the bottom quarter of the 12 pages compared (100 views or fewer); 70% of 100 readings engaged, in the top quarter (70% or more)" {
		t.Errorf("few views, much engagement = %v, want /quiet-held alone", c)
	}
	if rep.Compared != 13 || rep.Engagement != 12 {
		t.Errorf("compared %d, with engagement %d; want 13 and 12 (/busy-unread has 9 readings)", rep.Compared, rep.Engagement)
	}

	rep = ClassifyContent([]ContentPage{
		{Path: "/fell", Views: 20, PrevViews: 30},
		{Path: "/fell-less", Views: 21, PrevViews: 30},
		{Path: "/fell-from-few", Views: 0, PrevViews: 19},
		{Path: "/rose", Views: 30, PrevViews: 20},
		{Path: "/rose-less", Views: 29, PrevViews: 20},
		{Path: "/new", Views: 20},
		{Path: "/new-few", Views: 19},
		{Path: "/searched", Views: 20, PrevViews: 20, FromSearch: 10},
		{Path: "/searched-less", Views: 20, PrevViews: 20, FromSearch: 9},
		{Path: "/searched-few", Views: 19, PrevViews: 19, FromSearch: 19},
	})
	if c := category(rep, "falling"); len(c) != 1 || c["/fell"] != "20 views, against 30 in the days before (-33%)" {
		t.Errorf("falling = %v, want /fell alone, said with its change", c)
	}
	if c := category(rep, "rising"); len(c) != 2 || c["/rose"] != "30 views, against 20 in the days before (+50%)" || c["/new"] != "20 views, none in the days before" {
		t.Errorf("rising = %v, want /rose and /new", c)
	}
	if c := category(rep, "from_search"); len(c) != 1 || c["/searched"] != "10 of its 20 views arrived from a search engine (50%)" {
		t.Errorf("arrives from search = %v, want /searched alone", c)
	}
	// No page had readings or visits: the four comparisons with the site's
	// other pages say so, and list none.
	var why []string
	for _, c := range rep.Categories {
		if c.NotCompared != "" {
			if len(c.Pages) != 0 {
				t.Errorf("%s, not compared, lists %v", c.ID, c.Pages)
			}
			why = append(why, c.ID+": "+c.NotCompared)
		}
	}
	if want := []string{
		"many_views_little_engagement: 0 pages had 20 views and 10 readings, and a quarter is taken of 8 or more.",
		"many_views_much_engagement: 0 pages had 20 views and 10 readings, and a quarter is taken of 8 or more.",
		"few_views_much_engagement: 0 pages had 20 views and 10 readings, and a quarter is taken of 8 or more.",
		"converting: 0 pages were viewed in 20 visits or more, and a quarter is taken of 8 or more.",
	}; strings.Join(why, "\n") != strings.Join(want, "\n") {
		t.Errorf("not compared:\n%s\nwant\n%s", strings.Join(why, "\n"), strings.Join(want, "\n"))
	}
	if len(rep.Categories) != 7 {
		t.Errorf("%d categories, want all seven whether compared or not", len(rep.Categories))
	}
}

// Conversion is against the pages viewed in 20 visits or more, and a page
// with none converts at no rate, however the others fall. Of rates 0/40 to
// 7/40, the third quartile is the sixth, 5/40.
func TestConvertingIsTheTopQuarterOfVisitedPages(t *testing.T) {
	var pages []ContentPage
	for i := 0; i < 8; i++ {
		pages = append(pages, ContentPage{Path: fmt.Sprintf("/v%d", i), Views: 40, Visits: 40, Converted: int64(i)})
	}
	pages = append(pages, ContentPage{Path: "/few-visits", Views: 40, Visits: 19, Converted: 19})
	c := category(ClassifyContent(pages), "converting")
	if len(c) != 3 || c["/v5"] != "5 of its 40 visits had a custom event (13%), in the top quarter of the 8 pages compared (13% or more)" || c["/v6"] == "" || c["/v7"] == "" {
		t.Errorf("converting = %v, want /v5, /v6 and /v7", c)
	}
	none := make([]ContentPage, 8)
	for i := range none {
		none[i] = ContentPage{Path: fmt.Sprintf("/n%d", i), Views: 40, Visits: 40}
	}
	if c := category(ClassifyContent(none), "converting"); len(c) != 0 {
		t.Errorf("converting with no conversion anywhere = %v", c)
	}
}

// When every page has the same views, none has many or few, whatever its
// engagement; when every page is engaged alike, none has much or little,
// whatever its views. Each figure is tried alone, so each side's guard is.
func TestEqualPagesAreNeitherManyNorFew(t *testing.T) {
	sameViews := make([]ContentPage, 8)
	sameShare := make([]ContentPage, 8)
	for i := range sameViews {
		sameViews[i] = ContentPage{Path: fmt.Sprintf("/e%d", i), Views: 100, Readings: 100, Engaged: int64(10 * (i + 1))}
		sameShare[i] = ContentPage{Path: fmt.Sprintf("/s%d", i), Views: int64(100 * (i + 1)), Readings: 100, Engaged: 50}
	}
	for name, pages := range map[string][]ContentPage{"equal views": sameViews, "equal engagement": sameShare} {
		rep := ClassifyContent(pages)
		for _, id := range []string{"many_views_little_engagement", "many_views_much_engagement", "few_views_much_engagement"} {
			if c := category(rep, id); len(c) != 0 {
				t.Errorf("%s: %s = %v", name, id, c)
			}
		}
	}
}

// Seven pages compared are too few for a quarter of them to be a group.
func TestSevenPagesAreNotCompared(t *testing.T) {
	rep := ClassifyContent(compared()[:7])
	for _, c := range rep.Categories {
		if c.ID == "many_views_little_engagement" && (c.NotCompared != "7 pages had 20 views and 10 readings, and a quarter is taken of 8 or more." || len(c.Pages) != 0) {
			t.Errorf("seven pages: %+v", c)
		}
	}
}
