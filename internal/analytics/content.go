// SPDX-License-Identifier: Apache-2.0

package analytics

// content.go — what each page did over a range, sorted into the categories an
// editor reads: many views and little engagement, many and much, few and
// much, falling, rising, arriving from search, converting. A page is never
// called good or bad; each category is a measured rule, and each page in it
// says the figures that put it there and the threshold they passed.
//
// "Many" and "few" are against the site's own pages, by quartile: a page is
// compared only once it has enough views (and, for engagement, enough
// readings) for its figure to mean something, and only when enough pages are
// compared for a quarter of them to be a group. Falling, rising and arriving
// from search are against the page itself, so they need no other pages.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/vayuanalytics/classifier"
)

// The rules' sizes. A page is compared with views from contentMinViews, its
// engagement from contentMinReadings readings, and a quartile is taken over
// contentMinPages pages or more.
const (
	contentMinViews    = 20
	contentMinReadings = 10
	contentMinPages    = 8
	// ContentShown is how many pages each category lists, the most viewed
	// (or, falling and rising, the largest change) first.
	ContentShown = 25
)

// ContentPage is what was measured of one page over a range: its views and
// those of the range of equal length before, the visits that viewed it and
// those of them with a custom event, its views that arrived from a search
// engine, and the reading beacon's readings of it (page loads measured as the
// reader left) and how many of them were engaged. The caller fills Readings
// and Engaged from the engagement store.
type ContentPage struct {
	Path       string `json:"path"`
	Views      int64  `json:"views"`
	PrevViews  int64  `json:"views_before"`
	Visits     int64  `json:"visits"`
	Converted  int64  `json:"visits_converted"`
	FromSearch int64  `json:"views_from_search"`
	Readings   int64  `json:"readings"`
	Engaged    int64  `json:"readings_engaged"`
}

// ContentCategory is one category and the pages in it.
type ContentCategory struct {
	ID    string         `json:"id"`
	Label string         `json:"label"`
	Rule  string         `json:"rule"`
	Pages []ContentEntry `json:"pages"`
	// Total is how many pages met the rule; Pages lists ContentShown of them.
	Total int `json:"total"`
	// NotCompared says why the site's pages were too few for the rule to be
	// applied; the category then has no pages, which is not that none met it.
	NotCompared string `json:"not_compared,omitempty"`
}

// ContentEntry is a page in a category and why it is there.
type ContentEntry struct {
	ContentPage
	Reason string `json:"reason"`
}

// ContentReport is every category over a range, in the order the pages show
// them.
type ContentReport struct {
	Categories []ContentCategory `json:"categories"`
	// Compared is how many pages had views enough to be compared, and
	// Engagement how many of those had readings enough as well.
	Compared   int `json:"pages_compared"`
	Engagement int `json:"pages_with_engagement"`
}

// ContentPagesScoped is each page's views over the trailing days with the
// visits, conversions and search arrivals among them, and the views of the
// days before. A page is listed when it had any view in the range, or
// contentMinViews in the range before (it may have fallen to none).
func (s *Store) ContentPagesScoped(ctx context.Context, scope string, days int) ([]ContentPage, error) {
	if days <= 0 {
		days = 30
	}
	now := s.now().UTC()
	from := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	prevFrom := now.AddDate(0, 0, -(2*days - 1)).Format("2006-01-02")
	search, sargs := s.channelSQL("p.referrer")
	cclause, cargs := domainClauseOn("c", scope)
	pclause, pargs := domainClauseOn("p", scope)
	// In the order the query names them: the CTE's range and site, the
	// search expression in the SELECT, then the outer range and site.
	args := append(append(append(append([]any{from}, cargs...), sargs...), from), pargs...)
	rows, err := s.readDB().QueryContext(ctx, contentPagesSQL(search, cclause, pclause), args...)
	if err != nil {
		return nil, err
	}
	byPath := map[string]*ContentPage{}
	var order []string
	for rows.Next() {
		p := &ContentPage{}
		if err := rows.Scan(&p.Path, &p.Views, &p.Visits, &p.Converted, &p.FromSearch); err != nil {
			_ = rows.Close()
			return nil, err
		}
		byPath[p.Path] = p
		order = append(order, p.Path)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	rows, err = s.readDB().QueryContext(ctx, contentBeforeSQL(pclause), append(append([]any{prevFrom, from}, pargs...), contentMinViews)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		var n int64
		if err := rows.Scan(&path, &n); err != nil {
			return nil, err
		}
		p := byPath[path]
		if p == nil {
			p = &ContentPage{Path: path}
			byPath[path] = p
			order = append(order, path)
		}
		p.PrevViews = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ContentPage, 0, len(order))
	for _, path := range order {
		out = append(out, *byPath[path])
	}
	return out, nil
}

// contentPagesSQL and contentBeforeSQL are ContentPagesScoped's reads, each
// a range of a time index (console_plans_test.go reads their plans). The
// page is grouped through a unary +, as every report here is: the range on
// event_type and created_at already leads the planner to the time index, and
// the + keeps idx_apv_path out of its choices should table statistics ever
// favour it. A visit converts when it has a custom event, as the Explorer's
// "Visit with a custom event"; the visits with one are found once, then
// looked up.
func contentPagesSQL(search, cclause, pclause string) string {
	return `WITH conv AS (SELECT DISTINCT c.session_id AS id FROM analytics_pageviews c WHERE c.event_type=2 AND c.created_at>=?` + cclause + `)
		SELECT p.url_path, COUNT(1), COUNT(DISTINCT p.session_id),
			COUNT(DISTINCT CASE WHEN p.session_id IN (SELECT id FROM conv) THEN p.session_id END),
			SUM((` + search + `)='` + string(classifier.Organic) + `')
		FROM analytics_pageviews p WHERE p.event_type=1 AND p.created_at>=?` + pclause + ` GROUP BY +p.url_path`
}

func contentBeforeSQL(pclause string) string {
	return `SELECT p.url_path, COUNT(1) FROM analytics_pageviews p WHERE p.event_type=1 AND p.created_at>=? AND p.created_at<?` + pclause +
		` GROUP BY +p.url_path HAVING COUNT(1)>=?`
}

// quartiles are the nearest-rank first quartile, median and third quartile
// of v, which is sorted here.
func quartiles(v []float64) (q1, med, q3 float64) {
	sort.Float64s(v)
	at := func(q float64) float64 { return v[int(math.Ceil(q*float64(len(v))))-1] }
	return at(0.25), at(0.5), at(0.75)
}

// ClassifyContent sorts pages into the categories. high and low are a
// quartile's side and of the median's: when every page has the same figure,
// none is above or below the others, so none is "many" or "few", and when
// most pages convert at no rate, a page converting at none is not among the
// top, since it is not above the median.
func ClassifyContent(pages []ContentPage) ContentReport {
	var rep ContentReport
	var compared, engaged, visited []ContentPage
	for _, p := range pages {
		if p.Views < contentMinViews {
			continue
		}
		compared = append(compared, p)
		if p.Readings >= contentMinReadings {
			engaged = append(engaged, p)
		}
		if p.Visits >= contentMinViews {
			visited = append(visited, p)
		}
	}
	rep.Compared, rep.Engagement = len(compared), len(engaged)
	share := func(p ContentPage) float64 { return float64(p.Engaged) / float64(p.Readings) }
	conversion := func(p ContentPage) float64 { return float64(p.Converted) / float64(p.Visits) }

	var cats []ContentCategory
	add := func(id, label, rule string, in []ContentEntry, byChange bool) {
		sort.SliceStable(in, func(i, j int) bool {
			if byChange {
				ci, cj := absChange(in[i].ContentPage), absChange(in[j].ContentPage)
				if ci != cj {
					return ci > cj
				}
			} else if in[i].Views != in[j].Views {
				return in[i].Views > in[j].Views
			}
			return in[i].Path < in[j].Path
		})
		c := ContentCategory{ID: id, Label: label, Rule: rule, Total: len(in), Pages: in}
		if len(c.Pages) > ContentShown {
			c.Pages = c.Pages[:ContentShown]
		}
		cats = append(cats, c)
	}

	engagementRule := "Against the site's pages with " + strconv.Itoa(contentMinViews) + " views and " + strconv.Itoa(contentMinReadings) +
		" readings or more. A reading is engaged when the reader stayed 30 seconds and scrolled a quarter of the page."
	engagementCats := [][2]string{
		{"many_views_little_engagement", "Many views, little engagement"},
		{"many_views_much_engagement", "Many views, much engagement"},
		{"few_views_much_engagement", "Few views, much engagement"},
	}
	if len(engaged) >= contentMinPages {
		views := make([]float64, 0, len(engaged))
		shares := make([]float64, 0, len(engaged))
		for _, p := range engaged {
			views = append(views, float64(p.Views))
			shares = append(shares, share(p))
		}
		v1, vm, v3 := quartiles(views)
		e1, em, e3 := quartiles(shares)
		manyW := fmt.Sprintf("in the top quarter of the %d pages compared (%s views or more)", len(engaged), whole(v3))
		fewW := fmt.Sprintf("in the bottom quarter of the %d pages compared (%s views or fewer)", len(engaged), whole(v1))
		muchW := fmt.Sprintf("in the top quarter (%s or more)", pct(e3))
		littleW := fmt.Sprintf("in the bottom quarter (%s or less)", pct(e1))
		var manyLittle, manyMuch, fewMuch []ContentEntry
		for _, p := range engaged {
			v, e := float64(p.Views), share(p)
			many, few := v >= v3 && v > vm, v <= v1 && v < vm
			much, little := e >= e3 && e > em, e <= e1 && e < em
			reason := func(viewsSide, readSide string) string {
				return whole(v) + " views, " + viewsSide + "; " + pct(e) + " of " + strconv.FormatInt(p.Readings, 10) + " readings engaged, " + readSide
			}
			switch {
			case many && little:
				manyLittle = append(manyLittle, ContentEntry{p, reason(manyW, littleW)})
			case many && much:
				manyMuch = append(manyMuch, ContentEntry{p, reason(manyW, muchW)})
			case few && much:
				fewMuch = append(fewMuch, ContentEntry{p, reason(fewW, muchW)})
			}
		}
		for i, in := range [][]ContentEntry{manyLittle, manyMuch, fewMuch} {
			add(engagementCats[i][0], engagementCats[i][1], engagementRule, in, false)
		}
	} else {
		why := fmt.Sprintf("%d pages had %d views and %d readings, and a quarter is taken of %d or more.",
			len(engaged), contentMinViews, contentMinReadings, contentMinPages)
		for _, c := range engagementCats {
			cats = append(cats, ContentCategory{ID: c[0], Label: c[1], Rule: engagementRule, NotCompared: why})
		}
	}

	var falling, rising []ContentEntry
	for _, p := range pages {
		switch {
		case p.PrevViews >= contentMinViews && 3*p.Views <= 2*p.PrevViews:
			falling = append(falling, ContentEntry{p, fmt.Sprintf("%s views, against %s in the days before (%s)", whole(float64(p.Views)), whole(float64(p.PrevViews)), change(p))})
		case p.Views >= contentMinViews && 2*p.Views >= 3*p.PrevViews:
			before := "none in the days before"
			if p.PrevViews > 0 {
				before = fmt.Sprintf("against %s in the days before (%s)", whole(float64(p.PrevViews)), change(p))
			}
			rising = append(rising, ContentEntry{p, whole(float64(p.Views)) + " views, " + before})
		}
	}
	add("falling", "Falling", "Down by a third or more against the days before, from "+strconv.Itoa(contentMinViews)+" views or more.", falling, true)
	add("rising", "Rising", "Up by half or more against the days before, to "+strconv.Itoa(contentMinViews)+" views or more.", rising, true)

	var fromSearch []ContentEntry
	for _, p := range compared {
		if 2*p.FromSearch >= p.Views {
			fromSearch = append(fromSearch, ContentEntry{p, fmt.Sprintf("%s of its %s views arrived from a search engine (%s)",
				whole(float64(p.FromSearch)), whole(float64(p.Views)), pct(float64(p.FromSearch)/float64(p.Views)))})
		}
	}
	add("from_search", "Arrives from search", "Half or more of its views, from "+strconv.Itoa(contentMinViews)+
		", arrived from a search engine. How often search engines showed it is counted by them, not here.", fromSearch, false)

	convRule := "Against the site's pages viewed in " + strconv.Itoa(contentMinViews) +
		" visits or more. A visit converts when it has a custom event, as the Explorer counts it."
	if len(visited) >= contentMinPages {
		rates := make([]float64, 0, len(visited))
		for _, p := range visited {
			rates = append(rates, conversion(p))
		}
		_, cm, c3 := quartiles(rates)
		var converting []ContentEntry
		for _, p := range visited {
			if r := conversion(p); r >= c3 && r > cm {
				converting = append(converting, ContentEntry{p, fmt.Sprintf("%s of its %d visits had a custom event (%s), in the top quarter of the %d pages compared (%s or more)",
					strconv.FormatInt(p.Converted, 10), p.Visits, pct(r), len(visited), pct(c3))})
			}
		}
		add("converting", "Converts", convRule, converting, false)
	} else {
		cats = append(cats, ContentCategory{ID: "converting", Label: "Converts", Rule: convRule,
			NotCompared: fmt.Sprintf("%d pages were viewed in %d visits or more, and a quarter is taken of %d or more.", len(visited), contentMinViews, contentMinPages)})
	}
	rep.Categories = cats
	return rep
}

func absChange(p ContentPage) int64 {
	if d := p.Views - p.PrevViews; d >= 0 {
		return d
	}
	return p.PrevViews - p.Views
}

// change is the change from the views before as a signed whole percentage.
func change(p ContentPage) string {
	d := math.Round(100 * float64(p.Views-p.PrevViews) / float64(p.PrevViews))
	if d > 0 {
		return "+" + strconv.FormatFloat(d, 'f', 0, 64) + "%"
	}
	return strconv.FormatFloat(d, 'f', 0, 64) + "%"
}

// whole is a count with its thousands separated, as the console writes them.
func whole(v float64) string {
	s := strconv.FormatInt(int64(v), 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// pct is a share as a whole percentage.
func pct(v float64) string { return strconv.FormatFloat(math.Round(100*v), 'f', 0, 64) + "%" }
