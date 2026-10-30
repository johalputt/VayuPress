// SPDX-License-Identifier: Apache-2.0

package main

// analytics_page.go — one page's whole story: how its readers arrived, the
// page they came from and where they went next, which of its links they
// followed, how far down they read, how fast it was for them and what it
// led to. Every part is read from what is already recorded; the page report
// is the Analytics page narrowed to the visits that read the page, with the
// page's own figures beside.

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/ui"
	vastore "github.com/johalputt/vayupress/internal/vayuanalytics/store"
)

// pageReportHref is base's report on path under p, its period and filters
// kept.
func pageReportHref(base, path string, p analyticsSpan) string {
	p.page = path
	return analyticsHref(base, "page", p)
}

// renderPageReport is the report on path for v's site.
func (a *App) renderPageReport(ctx context.Context, v analyticsView, host string, days int, path string) string {
	st, site := a.analytics, v.scope
	per := spanOfCtx(ctx, days)
	per.page = path
	days = per.days
	readPage := analytics.ExploreFilter{Dim: "path", Op: analytics.FilterIs, Value: path}
	read, _ := per.with(readPage)
	// The visits that read the page: every list below counts them.
	visits := a.narrowed(ctx, read.focus)
	door := analyticsDoor(v, per)
	part := func(id string, h ui.HTML) ui.HTML { return ui.HTML(`<div data-part="`+id+`">`) + h + `</div>` }

	title := a.pageTitles(ctx, v, []analytics.PageStat{{Path: path}})[path]
	periodMenu, rangeSheet := osPeriodSelector(v.base, "page", per)
	page := ui.OverviewPage{Title: title, Actions: periodMenu,
		Tabs: ui.HTML(`<div class="sa-viewbar">`) + analyticsTabs(v.base, "page", per) + a.dataKept(ctx, v) + `</div>` + osFocusBar(v, "page", per)}

	views := func(m analytics.ExploreMetric, by string, extra ...analytics.ExploreFilter) *analytics.ExploreResult {
		res, err := st.Explore(ctx, analytics.ExploreQuery{Scope: site, Metric: m, By: by, Days: days, Filters: append([]analytics.ExploreFilter{readPage}, extra...)})
		if err != nil {
			return &analytics.ExploreResult{}
		}
		return res
	}
	pv, vis := views(analytics.ExploreViews, "day"), views(analytics.ExploreVisitors, "day")
	ov, err := st.OverviewSinceScoped(visits, site, days)
	if err != nil || pv.Total == 0 {
		page.State = ui.Text("No one read " + path + " " + per.when())
		return string(ui.Overview(page, ui.Band{}, rangeSheet))
	}
	exits, _ := st.Explore(ctx, analytics.ExploreQuery{Scope: site, Metric: analytics.ExploreVisits, Days: days,
		Filters: []analytics.ExploreFilter{{Dim: "exit", Op: analytics.FilterIs, Value: path}}})
	var reading vastore.PageRead
	if a.vaEngagement != nil {
		vsite := site
		if v.all() {
			vsite = vastore.AllDomains
		}
		reading, _ = a.vaEngagement.PageReading(ctx, vsite, path, days)
	}
	page.State = ui.Text(countOf(int(pv.Total), "view") + " of " + path + " " + per.when() + rankWords(ctx, st, site, days, path))
	figs := []ui.Figure{
		{Label: "Page views", Value: humanCount(int(pv.Total)), Def: analyticsDefs + "page-view"},
		{Label: "Visitors", Value: humanCount(int(vis.Total)), Def: analyticsDefs + "visitor"},
		{Label: "Visits that read it", Value: humanCount(ov.TotalVisits), Note: "Every page of them is counted below", Def: analyticsDefs + "visit"},
	}
	if ov.TotalVisits > 0 && exits != nil {
		figs = append(figs, ui.Figure{Label: "Left from here", Value: pctText(100 * exits.Total / float64(ov.TotalVisits)), Note: "Visits that ended on this page", Def: analyticsDefs + "exit-page"})
	}
	if reading.Readings > 0 {
		figs = append(figs, ui.Figure{Label: "Time on the page", Value: osDuration(reading.AvgSeconds), Note: "Over the readings that stayed any time", Def: analyticsDefs + "reading-depth"})
	}

	var sections []ui.HTML
	series := mergeDays(pv, vis)
	if trend := osTrendChart(series, "Views of this page and its visitors a day, "+per.phrase(), nil); trend != "" {
		sections = append(sections, part("trend", ui.Section("Every day", "Views of this page and its visitors", ui.HTML(trend))))
	}
	measured := func(dim string, n int, empty string) string {
		ms, err := st.VisitMeasuresScoped(visits, site, dim, days, n)
		if err != nil {
			return string(ui.Callout("danger", ui.Text("Not counted: "+err.Error())))
		}
		rows := make([]measuredRow, 0, len(ms))
		for _, m := range ms {
			label, hint := measuredLabel(dim, m.Key)
			rows = append(rows, measuredRow{Label: label, Hint: hint, Href: door(dim, m.Key), M: m})
		}
		return osMeasuredList(rows, "visits", ov, empty)
	}
	sections = append(sections, ui.HTML(analyticsCols(
		string(part("channels", ui.Section("How its readers arrived", "Visits that read it, by the referrer each arrived by", ui.HTML(measured("visit_channel", 10, "No visit arrived yet."))))),
		string(part("referrers", ui.Section("Referrers", "Visits from other sites that read it", ui.HTML(measured("visit_referrer", 15, "No visit came from another site."))))))))

	before, after := pageNeighbours(ctx, st, site, days, path)
	outs := views(analytics.ExploreOutbound, "event")
	downloads := views(analytics.ExploreDownloads, "event")
	sections = append(sections, part("journey", ui.Section("Before and after", "The page read just before it in the visit, and what came next", ui.HTML(analyticsCols(
		analyticsPanel("Came from", osBarList(pathBars(before, door, v.base, per), osShareOfListed(), "Every visit began on this page.")),
		analyticsPanel("Went next", osBarList(append(pathBars(after, door, v.base, per), eventBars(outs, "Out to ", door)...), osShareOfListed(), "No reader went on from this page.")),
		analyticsPanel("Downloads", osBarList(eventBars(downloads, "", door), osShareHidden(), "No file was fetched from this page.")))))))

	if links := a.pageLinks(ctx, host, path); len(links) > 0 {
		sections = append(sections, part("links", ui.Section("Links on this page", "Each link, in the page's order, and how many followed it",
			ui.HTML(osLinkMap(links, after, outs, ov.TotalVisits)))))
	}
	if reading.Readings > 0 {
		sections = append(sections, part("reach", ui.Section("How far they read", countOf(int(reading.Readings), "reading")+", measured as the reader left",
			ui.HTML(osReach(reading)))))
	}
	if a.vaEngagement != nil {
		vsite := site
		if v.all() {
			vsite = vastore.AllDomains
		}
		if w, err := a.vaEngagement.WebVitalsP75ForPage(ctx, vsite, path, days); err == nil && w.Samples > 0 {
			sections = append(sections, part("speed", ui.Section("How fast it was", "What readers' browsers measured on this page, the 75th percentile", ui.HTML(osVitalsTable(w)))))
		}
	}
	if goals, err := st.GoalResultsOver(visits, v.defs, site, days); err == nil && len(goals) > 0 {
		bars := make([]osChartBar, 0, len(goals))
		for _, g := range goals {
			bars = append(bars, osChartBar{Label: g.Name, Value: g.UniqueVisitors})
		}
		sections = append(sections, part("goals", ui.Section("What it led to", "Visitors who read it and reached each goal",
			ui.HTML(osBarList(bars, osShareOf(ov.UniqueVisitors), "")))))
	}
	sections = append(sections, rangeSheet)
	return string(ui.Overview(ui.OverviewPage{Title: page.Title, State: page.State, Actions: page.Actions, Tabs: page.Tabs},
		ui.Band{Title: "This page", Figures: figs}, sections...))
}

// rankWords is the page's place among the site's most read, when it is
// among the first hundred.
func rankWords(ctx context.Context, st *analytics.Store, site string, days int, path string) string {
	top, err := st.TopPagesScoped(ctx, site, days, 100)
	if err != nil {
		return ""
	}
	for i, p := range top {
		if p.Path == path {
			if i == 0 {
				return ", the site's most read"
			}
			return ", " + ordinal(i+1) + " most read"
		}
	}
	return ""
}

func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

// mergeDays is a page's views and visitors a day as the trend chart reads
// them.
func mergeDays(pv, vis *analytics.ExploreResult) []analytics.DayPageviews {
	visitors := map[string]int{}
	for _, r := range vis.Rows {
		visitors[r.Key] = int(r.Value)
	}
	out := make([]analytics.DayPageviews, 0, len(pv.Rows))
	for _, r := range pv.Rows {
		out = append(out, analytics.DayPageviews{Date: r.Key, Count: int(r.Value), Visitors: visitors[r.Key]})
	}
	return out
}

// pageNeighbours is the pages read just before path in a visit, and just
// after it, with how many times, most first.
func pageNeighbours(ctx context.Context, st *analytics.Store, site string, days int, path string) (before, after []analytics.AudienceStat) {
	flows, err := st.PathFlowsScoped(ctx, site, days, 1000)
	if err != nil {
		return nil, nil
	}
	for _, f := range analytics.BetweenPages(flows) {
		switch {
		case f.To == path && f.From != path:
			before = append(before, analytics.AudienceStat{Label: f.From, Count: f.Count})
		case f.From == path && f.To != path:
			after = append(after, analytics.AudienceStat{Label: f.To, Count: f.Count})
		}
	}
	return before, after
}

// pathBars is pages as bars, each the way into the visits that read it and
// to its own report on base under p.
func pathBars(items []analytics.AudienceStat, door func(dim, value string) string, base string, p analyticsSpan) []osChartBar {
	out := make([]osChartBar, 0, len(items))
	for _, it := range items {
		out = append(out, osChartBar{Label: prettyPathText(it.Label), Value: it.Count, Hint: it.Label, Href: door("path", it.Label), Report: pageReportHref(base, it.Label, p)})
	}
	return out
}

// eventBars is links out or downloads as bars, prefix before each, each
// the way into the visits that followed it.
func eventBars(res *analytics.ExploreResult, prefix string, door func(dim, value string) string) []osChartBar {
	out := make([]osChartBar, 0, len(res.Rows))
	for _, r := range res.Rows {
		out = append(out, osChartBar{Label: prefix + r.Key, Value: int(r.Value), Hint: r.Key, Href: door("event", r.Key)})
	}
	return out
}

// pageLink is one link on a page: its words, where it goes as written, and
// the key it is counted under, a path on the site or a link out's
// "host/path" (analytics.LinkTarget).
type pageLink struct {
	Text, Href, Key string
	Out             bool
}

// pageLinks is the links in path's content as host serves it to a reader,
// in order, read from the page itself through the router (previewFetch):
// the article's or the main region's, never the header's, the navigation's
// or the footer's, which are on every page.
func (a *App) pageLinks(ctx context.Context, host, path string) []pageLink {
	h := a.rootHandler()
	if h == nil || host == "" {
		return nil
	}
	body, res := previewFetch(ctx, h, host, path)
	if res.Code != 200 || !strings.Contains(res.Header().Get("Content-Type"), "text/html") {
		return nil
	}
	return linksIn(body, host, path)
}

// linksIn is the links in a page's content, as pageLinks reads them.
func linksIn(body, host, path string) []pageLink {
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	root := findElement(doc, atom.Article)
	if root == nil {
		root = findElement(doc, atom.Main)
	}
	if root == nil {
		root = findElement(doc, atom.Body)
	}
	if root == nil {
		return nil
	}
	base := &url.URL{Scheme: "https", Host: host, Path: path}
	var out []pageLink
	seen := map[string]bool{}
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			switch n.DataAtom {
			case atom.Nav, atom.Header, atom.Footer, atom.Script, atom.Style:
				return
			case atom.A:
				if l, ok := linkOf(n, base, host); ok && !seen[l.Key] && len(out) < 100 {
					seen[l.Key] = true
					out = append(out, l)
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

func findElement(n *xhtml.Node, a atom.Atom) *xhtml.Node {
	if n.Type == xhtml.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findElement(c, a); f != nil {
			return f
		}
	}
	return nil
}

// linkOf is an <a>'s link, keyed as it is counted, or false for one that
// goes nowhere a reader is counted going (an anchor on the page, mail, a
// script).
func linkOf(n *xhtml.Node, base *url.URL, host string) (pageLink, bool) {
	href := ""
	for _, at := range n.Attr {
		if at.Key == "href" {
			href = strings.TrimSpace(at.Val)
		}
	}
	u, err := base.Parse(href)
	if href == "" || strings.HasPrefix(href, "#") || err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return pageLink{}, false
	}
	text := strings.Join(strings.Fields(textOf(n)), " ")
	if len([]rune(text)) > 80 {
		text = string([]rune(text)[:79]) + "…"
	}
	if text == "" {
		text = href
	}
	if strings.EqualFold(u.Hostname(), host) {
		return pageLink{Text: text, Href: href, Key: analytics.NormalizePath(u.EscapedPath())}, true
	}
	key, ok := analytics.LinkTarget(u.String(), host, false)
	return pageLink{Text: text, Href: href, Key: key, Out: true}, ok
}

func textOf(n *xhtml.Node) string {
	if n.Type == xhtml.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c))
		b.WriteString(" ")
	}
	return b.String()
}

// osLinkMap is a page's links in its order, each with how many readers of
// the page followed it next (a page of the site) or out (a link off it), as
// a share of the visits that read the page.
func osLinkMap(links []pageLink, after []analytics.AudienceStat, outs *analytics.ExploreResult, visits int) string {
	next := map[string]int{}
	for _, a := range after {
		next[a.Label] += a.Count
	}
	for _, r := range outs.Rows {
		next[r.Key] += int(r.Value)
	}
	max := 1
	for _, l := range links {
		if next[l.Key] > max {
			max = next[l.Key]
		}
	}
	var b strings.Builder
	b.WriteString(`<ol class="sa-links">`)
	for _, l := range links {
		n := next[l.Key]
		where := l.Key
		if l.Out {
			where = "Out to " + l.Key
		}
		b.WriteString(`<li class="vp-bar"><span class="vp-bar__label" title="` + html.EscapeString(where) + `">` + html.EscapeString(l.Text) +
			` <span class="sa-links__to">` + html.EscapeString(where) + `</span></span><span class="vp-bar__val">` + humanCount(n))
		if visits > 0 {
			b.WriteString(`<span class="vp-bar__pct">` + osShareLabel(n, visits) + `</span>`)
		}
		b.WriteString(`</span><span class="vp-bar__track"><span class="vp-bar__fill ` + barWidthClass(n*100/max) + `"></span></span></li>`)
	}
	b.WriteString(`</ol>`)
	return b.String()
}

// osReach is how far down the page its readings got, a tenth at a time.
func osReach(r vastore.PageRead) string {
	bars := make([]osChartBar, 0, 10)
	for i, n := range r.Reach {
		label := strconv.Itoa((i+1)*10) + "% down"
		if i == 9 {
			label = "The end"
		}
		bars = append(bars, osChartBar{Label: label, Value: int(n)})
	}
	return osBarList(bars, osShareOf(int(r.Readings)), "")
}

// pageOf is the page r's report is about, as the event log keeps a path,
// or "" when it names none.
func pageOf(r *http.Request) string {
	p := strings.TrimSpace(r.URL.Query().Get("page"))
	if p == "" || !strings.HasPrefix(p, "/") || len(p) > 500 {
		return ""
	}
	return analytics.NormalizePath(p)
}
