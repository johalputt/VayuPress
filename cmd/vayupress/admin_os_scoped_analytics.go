// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scoped_analytics.go — one hosted domain's own traffic
// (ADR-0153 Phase 6).
//
// Migration 080 put the domain into the rolled-up daily counters and left the
// EVENT LOG alone — and the event log is what the panel, Top pages, trending and
// the overview all read. So per-domain traffic was half-built: the figure on a
// client's own page was scoped, and every figure on the operator's Analytics
// page counted every site on the install together.
//
// Migration 084 gives analytics_pageviews its domain, attributed SERVER-SIDE
// from the host this install resolved. Never from the beacon: that endpoint is
// public and unauthenticated, so a domain a visitor can name is a domain a
// visitor can write traffic into.

import (
	"context"
	htmpl "html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/alerts"
	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/span"
	"github.com/johalputt/vayupress/internal/ui"
)

// handleOSAnalytics is the primary site's Analytics: its own visits alone,
// read as a hosted site's are, by every reader given the primary's scope.
// Every site added together is Platform's (handleOSPlatformAnalytics).
func (a *App) handleOSAnalytics(w http.ResponseWriter, r *http.Request) {
	a.writeSiteAnalytics(w, r, primaryView, a.primaryHost(r.Context()), "Analytics", "analytics")
}

// handleOSScopedAnalytics renders one hosted site's Analytics.
func (a *App) handleOSScopedAnalytics(w http.ResponseWriter, r *http.Request) {
	d, ok := osScopedDomain(r)
	if !ok {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	a.writeSiteAnalytics(w, r, hostedView(d), d.Host, "Visitors · "+d.Host, "optimize")
}

// writeSiteAnalytics is one site's Analytics page for v, whose host is host.
func (a *App) writeSiteAnalytics(w http.ResponseWriter, r *http.Request, v analyticsView, host, title, app string) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	if a.analytics == nil {
		writeOSHTML(w, r, adminOSLayout(nonce, title, app, cfg, ui.Overview(ui.OverviewPage{Title: v.title()},
			ui.Band{Title: "Visitors", Aside: ui.Empty("trend", "Analytics are off", "This install was started without the analytics store.", "")})))
		return
	}
	per := analyticsSpanOf(r)
	days := per.days
	view := analyticsViewOf(r)
	// Counted off the request, in the cache the install's Analytics uses: an
	// exact count of a busy site's visitors over a month reads every view in
	// it (0.3 s at a million events, 1.1 s for the whole install), and no
	// console page waits on that.
	body, ready := adminDash.get(siteAnalyticsKey(v, view, per), analyticsFragmentTTL, func(ctx context.Context) string {
		return a.renderSiteAnalyticsBody(a.scoped(ctx, per), v, host, days, view)
	})
	if !ready {
		// The page still being counted offers the periods, not the range
		// sheet: a sheet with no page under it.
		menu, _ := osPeriodSelector(v.base, view, per)
		body = string(ui.Overview(ui.OverviewPage{Title: v.title(), State: ui.Text("Counting " + per.phrase() + " for " + host),
			Actions: menu, Tabs: analyticsTabs(v.base, view, per) + osFocusBar(v, view, per)},
			ui.Band{Title: "Visitors", Chart: `<p class="table-empty">This is counted in the background; reload in a few seconds.</p>`}))
	}
	// Goals holds what is set rather than counted: the alerts, and a hosted
	// site's retention. They are read off the request, not the cache, so a
	// save shows at once. The primary keeps the install's retention, set in
	// Settings.
	if view == "goals" {
		body += a.alertsSection(r.Context(), v.alert, v.alertsAPI(), cfg.AccessLevel >= accessAdmin, nonce)
		// The install's alerts count every site together, so they are kept on
		// every site's page; an administrator made them here before, and is
		// told where they went.
		if !v.hosted && cfg.AccessLevel >= accessAdmin && a.alerts != nil {
			if all, _ := a.alerts.List(r.Context(), platformView.alert); len(all) > 0 {
				body += `<p class="settings-row-hint">Alerts on every site together: <a href="` + alertPage(alerts.Install) + `">` + string(ui.Text(platformAnalyticsTitle)) + `</a>.</p>`
			}
		}
		if v.hosted {
			body += a.siteRetentionSection(r.Context(), v.d, nonce)
		}
	}
	// How this works is the page's, said once, under the overview: a view's
	// own sections say what they count, and Where they are has its own.
	if view == "overview" {
		body += string(analyticsExplain(v, host))
	}
	// The live panel's poller; the body above is cached, the script is not.
	body += `<script nonce="` + nonce + `" src="/os/static/js/admin-os-intel.js?v=` + assetVer("js/admin-os-intel.js") + `"></script>`
	if len(per.focus) > 0 {
		// The filter bar's Save as segment.
		body += segmentsScript(nonce)
	}
	writeOSHTML(w, r, adminOSLayout(nonce, title, app, cfg, htmpl.HTML(body)))
}

// analyticsExplain is How this works, last on the page: after the cached
// report and the sections read off the request (retention, alerts), so it
// closes the page rather than sitting between two of its folds.
func analyticsExplain(v analyticsView, host string) ui.HTML {
	explain := `<p>These are this site's own figures: every count reads only the visits recorded for ` + esc(host) + `.`
	if v.all() {
		explain = `<p>These figures add up every site this install serves.`
	}
	return ui.Explain(ui.HTML(explain + ` A visit is a reader's pages with no 30-minute gap; a visitor is recognised within a day by a salted hash that changes daily, so "visitors" over a week counts a person once for each day they came. Nothing here uses a cookie, and nothing leaves this server. <a href="/docs/ANALYTICS" target="_blank" rel="noopener">What each figure means</a>.</p>`))
}

// analyticsViews are an Analytics page's sections, in the order its bar
// lists them. Each is its own view (?view=), counted and cached on its own:
// thirteen folds on one page gave no way to reach a part but by scrolling
// past the rest, and every visit counted all of them.
var analyticsViews = []struct{ id, label string }{
	{"overview", "Overview"}, {"audience", "Audience"}, {"sources", "Sources"}, {"content", "Content"},
	{"behaviour", "Behaviour"}, {"performance", "Performance"}, {"goals", "Goals"}, {"live", "Live"},
}

// analyticsViewOf is the view r asks for, or the overview.
func analyticsViewOf(r *http.Request) string {
	want := r.URL.Query().Get("view")
	for _, v := range analyticsViews {
		if v.id == want {
			return want
		}
	}
	return "overview"
}

// analyticsHref is base's page for view over the period p: the overview
// and the default period are left out, so the page's own address stays as
// it was.
func analyticsHref(base, view string, p analyticsSpan) string {
	q := url.Values{}
	if view != "overview" {
		q.Set("view", view)
	}
	p.set(q)
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}

// analyticsTabs is the section bar: each view a link, the period kept.
func analyticsTabs(base, view string, p analyticsSpan) ui.HTML {
	tabs := make([]ui.Tab, 0, len(analyticsViews))
	for _, v := range analyticsViews {
		tabs = append(tabs, ui.Tab{Label: v.label, Href: analyticsHref(base, v.id, p), Current: v.id == view})
	}
	return ui.Tabs("Analytics", tabs...)
}

// siteAnalyticsKey is the cache key of v's view for a period, under
// siteAnalyticsPrefix: its scope, "" for the primary.
func siteAnalyticsKey(v analyticsView, view string, p analyticsSpan) string {
	return siteAnalyticsPrefix(v) + view + ":" + p.key()
}

func siteAnalyticsPrefix(v analyticsView) string { return "site-analytics:" + v.scope + ":" }

// renderSiteAnalyticsBody is one view of v's report: one site's, or every
// site's on Platform. Every reader is given v.scope (a site's id as the
// ingest wrote it, "" for the primary, or every site), so nothing here can
// count another site's traffic. A view counts only what it shows.
func (a *App) renderSiteAnalyticsBody(ctx context.Context, v analyticsView, host string, days int, view string) string {
	st := a.analytics
	per := spanOfCtx(ctx, days)
	days = per.days
	site, explore, phrase := v.scope, v.base+"/explore", per.phrase()
	title, whose := v.title(), host
	var scopeNote ui.HTML
	if v.all() {
		n, note := a.analyticsScopeNote(ctx)
		if n > 1 {
			whose = strconv.Itoa(n) + " sites"
		}
		scopeNote = note
	}
	exportBtn, exportSheet := osExportSheet(v.base+"/export", per)
	periodMenu, rangeSheet := osPeriodSelector(v.base, view, per)
	page := ui.OverviewPage{Title: title, Actions: periodMenu + exploreButton(explore, per) + exportBtn,
		Tabs: ui.HTML(`<div class="sa-viewbar">`) + analyticsTabs(v.base, view, per) + a.dataKept(ctx, v) + `</div>` + osFocusBar(v, view, per)}
	ov, err := st.OverviewSinceScoped(ctx, site, days)
	if err != nil {
		page.State = ui.Text("The figures could not be read: " + err.Error())
		return string(ui.Overview(page, ui.Band{}))
	}
	page.State = ui.Text("No visits to " + whose + " " + per.when())
	if ov.UniqueVisitors > 0 {
		page.State = ui.Text(countOf(ov.UniqueVisitors, "visitor") + " to " + whose + " " + per.when())
	}

	door := analyticsDoor(v, per)
	audience := func(col string) []analytics.AudienceStat {
		out, _ := st.AudienceScoped(ctx, site, col, days)
		return out
	}
	pages := func(n int) string {
		top, _ := st.TopPagesScoped(ctx, site, days, n)
		titles := a.pageTitles(ctx, v, top)
		bars := make([]osChartBar, 0, len(top))
		for _, p := range top {
			bars = append(bars, osChartBar{Label: titles[p.Path], Value: p.Pageviews, Hint: p.Path, Href: door("path", p.Path)})
		}
		return osBarList(bars, osShareOf(ov.TotalPageviews), "No page views recorded yet.")
	}
	// A list of a visit's own dimension carries what its visits did, to be
	// ranked by any of it (analytics_measures.go); first is its count.
	measured := func(dim, first string, n int, empty string) string {
		ms, err := st.VisitMeasuresScoped(ctx, site, dim, days, n)
		if err != nil {
			return string(ui.Callout("danger", ui.Text("Not counted: "+err.Error())))
		}
		rows := make([]measuredRow, 0, len(ms))
		for _, m := range ms {
			label, hint := measuredLabel(dim, m.Key)
			rows = append(rows, measuredRow{Label: label, Hint: hint, Href: door(dim, m.Key), M: m})
		}
		return osMeasuredList(rows, first, ov, empty)
	}
	channels := func() string {
		return measured("visit_channel", "visits", 10, "Visits are grouped here once they arrive.")
	}
	landingPages := func(n int) string { return measured("landing", "visits", n, "No visits recorded yet.") }
	more := func(label, to string) string {
		return `<a class="sa-more" href="` + esc(analyticsHref(v.base, to, per)) + `">` + esc(label) + ` →</a>`
	}
	part := func(id string, h ui.HTML) ui.HTML { return ui.HTML(`<div data-part="`+id+`">`) + h + `</div>` }

	var band ui.Band
	var sections []ui.HTML
	switch view {
	case "overview":
		// The period before is as long, and ends where this one begins.
		from, until := span.Days(ctx, time.Now(), days)
		start, _ := time.Parse("2006-01-02", from)
		prev, _ := st.OverviewBetweenScoped(ctx, site, start.AddDate(0, 0, -days).Format("2006-01-02"), from)
		series, _ := st.PageviewSeriesScoped(ctx, site, days)
		// The figures take the page's width, in one row; the days and who is
		// reading now sit beneath, side by side.
		band = ui.Band{Title: "Visitors", Figures: visitorFigures(ov, prev)}
		trend := osTrendChart(series, "Page views and visitors a day, "+phrase, a.postsByDay(ctx, v, from, until))
		if trend == "" {
			trend = `<p class="table-empty">The days are drawn once two of them have views.</p>`
		}
		sections = append(sections, ui.HTML(`<div class="sa-cols sa-cols--lead">`)+
			part("trend", ui.Section("Every day", "Page views and visitors", ui.HTML(trend)))+
			part("now", ui.Section("Right now", "", ui.HTML(osLiveCompact(v.base+"/realtime"+per.focusQuery(), analyticsHref(v.base, "live", per)))))+`</div>`)
		if v.all() {
			sections = append(sections, scopeNote, part("each-site", ui.HTML(a.osEachSiteSection(ctx, days))))
		}
		cols := []string{
			string(part("top-pages", ui.Section("Top pages", "Page views", ui.HTML(pages(5)+more("All pages", "content"))))),
		}
		if len(per.focus) > 0 {
			// Narrowed to some readers, the overview says where they began as
			// well as what they read.
			cols = append(cols, string(part("landing", ui.Section("Landing pages", "Where their visits began", ui.HTML(landingPages(5)+more("All landing pages", "content"))))))
		}
		cols = append(cols, string(part("channels", ui.Section("How they found you", "Visits, by the referrer each arrived by", ui.HTML(channels()+more("All sources", "sources"))))))
		sections = append(sections, ui.HTML(analyticsCols(cols...)))
	case "audience":
		sections = append(sections,
			part("devices", ui.Section("What they read on", "Devices, browsers and systems", ui.HTML(analyticsCols(
				analyticsPanel("Devices", measured("device", "visitors", 100, "No device recorded yet.")),
				analyticsPanel("Browsers", measured("browser", "visitors", 100, "No browser recorded yet.")),
				analyticsPanel("Systems", measured("os", "visitors", 100, "No system recorded yet.")))))),
			part("location", ui.Section("Where they are", "Country, region and city, coarse only", ui.HTML(osGeoSection(audience("country"), audience("region"), audience("city"), door)))))
	case "sources":
		utm, _ := st.UTMStatsScoped(ctx, site, days)
		sections = append(sections,
			ui.HTML(analyticsCols(
				string(part("referrers", ui.Section("Referrers", "Visits from other sites", ui.HTML(measured("visit_referrer", "visits", 25, "No links from other sites yet."))))),
				string(part("channels", ui.Section("How they found you", "Visits, by the referrer each arrived by", ui.HTML(channels())))))),
			part("campaigns", ui.Section("Campaigns", countOf(len(utm), "tagged source"), ui.HTML(osCampaigns(utm, door)))))
	case "content":
		sections = append(sections,
			ui.HTML(analyticsCols(
				string(part("top-pages", ui.Section("Top pages", "Page views", ui.HTML(pages(25))))),
				string(part("landing", ui.Section("Landing pages", "Where visits began", ui.HTML(landingPages(25))))))),
			part("content", ui.HTML(a.osContentSection(ctx, site, explore, days, door))),
			part("searches", ui.HTML(a.osSearchesSection(ctx, site, days, door))))
	case "behaviour":
		events, _ := st.CustomEventsScoped(ctx, site, days)
		eventBars := make([]osChartBar, 0, len(events))
		eventTotal := 0
		for _, e := range events {
			eventBars = append(eventBars, osChartBar{Label: e.Name, Value: e.Count, Href: door("event", e.Name)})
			eventTotal += e.Count
		}
		sections = append(sections,
			part("behaviour", ui.HTML(a.osBehaviourSection(ctx, site, explore, days, door))),
			part("journeys", ui.HTML(a.osJourneySection(ctx, site, days))),
			part("events", ui.Section("Events", countOf(eventTotal, "custom event"), ui.HTML(osBarList(eventBars, osShareHidden(), "No custom events yet.")))))
	case "performance":
		sections = append(sections, part("performance", ui.HTML(a.osPerformanceSection(ctx, site, days))))
	case "goals":
		sections = append(sections,
			part("goals", ui.HTML(a.osGoalsSectionOf(ctx, v, days))),
			part("segments", ui.HTML(a.osSegmentsSection(ctx, v, days, per))))
	case "live":
		sections = append(sections, part("live", ui.Section("Right now", "Readers in the last 5 minutes, every 10 seconds", ui.HTML(osLiveCard(v.base+"/realtime"+per.focusQuery(), liveDoor(v, per))))))
	}
	sections = append(sections, exportSheet, rangeSheet)
	return string(ui.Overview(page, band, sections...))
}

// analyticsDefs is where each figure is defined (docs/ANALYTICS.md), so a
// figure's label opens its definition.
const analyticsDefs = "/docs/ANALYTICS#"

// visitorFigures is the band's figures: visitors and page views, the visits,
// how many stopped at one page, and how long the others stayed, each with its
// change on the period before and its definition. No visitor is said once, in
// the page's state, not as a row of zeros.
func visitorFigures(ov, prev *analytics.Overview) []ui.Figure {
	if ov == nil || ov.UniqueVisitors == 0 {
		return nil
	}
	if prev == nil {
		prev = &analytics.Overview{}
	}
	figs := []ui.Figure{
		{Value: humanCount(ov.UniqueVisitors), Label: "Visitors", Note: analyticsChange(ov.UniqueVisitors, prev.UniqueVisitors), Def: analyticsDefs + "visitor"},
		{Value: humanCount(ov.TotalPageviews), Label: "Page views", Note: analyticsChange(ov.TotalPageviews, prev.TotalPageviews), Def: analyticsDefs + "page-view"},
	}
	if ov.TotalVisits > 0 {
		figs = append(figs,
			ui.Figure{Value: humanCount(ov.TotalVisits), Label: "Visits", Note: analyticsChange(ov.TotalVisits, prev.TotalVisits), Def: analyticsDefs + "visit"},
			ui.Figure{Value: strconv.FormatFloat(ov.BounceRate, 'f', 0, 64) + "%", Label: "Bounce rate", Note: "Visits of one page and nothing else", Def: analyticsDefs + "bounce"})
	}
	if ov.AvgDuration > 0 {
		figs = append(figs, ui.Figure{Value: osDuration(ov.AvgDuration), Label: "Average visit", Note: "Over the visits of more than one page", Def: analyticsDefs + "average-visit"})
	}
	return figs
}

// osDuration writes a visit's length as a reader would say it.
func osDuration(sec float64) string {
	s := int(sec + 0.5)
	if s < 60 {
		return strconv.Itoa(s) + "s"
	}
	return strconv.Itoa(s/60) + "m " + strconv.Itoa(s%60) + "s"
}

// pageTitles names each listed page as its post does, so a list of pages
// reads as titles rather than slugs cut at forty characters; the path stays
// in the row's tooltip. A path that is not a post (the home page, a tag, a
// page of a custom site) keeps its path. Posts are looked up on v's own
// site; Platform's lists, which add every site up, look on any.
func (a *App) pageTitles(ctx context.Context, v analyticsView, pages []analytics.PageStat) map[string]string {
	out := make(map[string]string, len(pages))
	slugs := make([]any, 0, len(pages))
	bySlug := map[string]string{}
	for _, p := range pages {
		out[p.Path] = prettyPathText(p.Path)
		// A path with more than one segment matches no slug and is simply
		// not found.
		slug := strings.TrimSuffix(strings.TrimPrefix(p.Path, "/"), "/")
		if slug != "" {
			slugs = append(slugs, slug)
			bySlug[slug] = p.Path
		}
	}
	if len(slugs) == 0 || dbpkg.DB == nil {
		return out
	}
	q := `SELECT slug,title FROM articles WHERE slug IN (?` + strings.Repeat(",?", len(slugs)-1) + `)`
	args := slugs
	if !v.all() {
		q += ` AND domain_id=?`
		args = append(args, v.scope)
	}
	rows, err := dbpkg.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var slug, title string
		if rows.Scan(&slug, &title) == nil && strings.TrimSpace(title) != "" {
			out[bySlug[slug]] = strings.TrimSpace(title)
		}
	}
	// A read cut short leaves the rest named by their paths, as a page with
	// no article is: nothing on the page is wrong, only plainer.
	_ = rows.Err()
	return out
}

// postsByDay is the titles of v's posts published in [from, until), by
// day, oldest first, for the chart's marks: a hosted site's own, the
// primary's, or every site's on Platform.
func (a *App) postsByDay(ctx context.Context, v analyticsView, from, until string) map[string][]string {
	out := map[string][]string{}
	if dbpkg.DB == nil {
		return out
	}
	q := `SELECT date(created_at), title FROM articles WHERE status='published' AND is_page=0 AND created_at>=? AND created_at<?`
	args := []any{from, until}
	if !v.all() {
		q += ` AND domain_id=?`
		args = append(args, v.scope)
	}
	rows, err := dbpkg.Reader().QueryContext(ctx, q+` ORDER BY created_at LIMIT 1000`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var day, title string
		if rows.Scan(&day, &title) == nil && strings.TrimSpace(title) != "" {
			out[day] = append(out[day], strings.TrimSpace(title))
		}
	}
	// A read cut short marks fewer days; the lines are the same.
	_ = rows.Err()
	return out
}

// dataKept is how long v's figures are kept, at the end of the section bar:
// a hosted site's own setting, a link to it on Goals; the install's, which
// its environment sets (ANALYTICS_RETAIN_DAYS), as words.
func (a *App) dataKept(ctx context.Context, v analyticsView) ui.HTML {
	if v.hosted {
		n, _ := a.siteRetainDays(ctx, v.d.ID)
		return ui.HTML(`<a class="sa-kept" href="` + esc(v.base+"?view=goals#retention") + `">Data kept ` + esc(countOf(n, "day")) + `</a>`)
	}
	return ui.HTML(`<span class="sa-kept" title="Set for this install by ANALYTICS_RETAIN_DAYS">Data kept ` + esc(countOf(config.Cfg.AnalyticsRetainDays, "day")) + `</span>`)
}
