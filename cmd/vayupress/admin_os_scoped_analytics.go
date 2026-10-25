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
	"strconv"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/render"
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
	days, _ := analyticsPeriod(r)
	// Counted off the request, in the cache the install's Analytics uses: an
	// exact count of a busy site's visitors over a month reads every view in
	// it (0.3 s at a million events, 1.1 s for the whole install), and no
	// console page waits on that.
	body, ready := adminDash.get(siteAnalyticsKey(v, days), analyticsFragmentTTL, func(ctx context.Context) string {
		return a.renderSiteAnalyticsBody(ctx, v, host, days)
	})
	if !ready {
		body = string(ui.Overview(ui.OverviewPage{Title: v.title(), State: ui.Text("Counting " + analyticsPhrase(days) + " for " + host),
			Actions: osPeriodSelector(v.base, days)},
			ui.Band{Title: "Visitors", Chart: `<p class="table-empty">This is counted in the background; reload in a few seconds.</p>`}))
	}
	// The settings are read off the request, not the cache, so a save shows
	// at once. The primary keeps the install's retention, set in Settings.
	if v.hosted {
		body += a.siteRetentionSection(r.Context(), v.d, nonce)
	}
	body += a.alertsSection(r.Context(), v.alert, v.alertsAPI(), cfg.AccessLevel >= accessAdmin, nonce)
	// The install's alerts count every site together, so they are kept on
	// every site's page; an administrator made them here before, and is told
	// where they went.
	if !v.hosted && cfg.AccessLevel >= accessAdmin && a.alerts != nil {
		if all, _ := a.alerts.List(r.Context(), platformView.alert); len(all) > 0 {
			body += `<p class="settings-row-hint">Alerts on every site together: <a href="` + platformAnalyticsBase + `#alerts">` + string(ui.Text(platformAnalyticsTitle)) + `</a>.</p>`
		}
	}
	body += string(analyticsExplain(v, host))
	// The live panel's poller; the body above is cached, the script is not.
	body += `<script nonce="` + nonce + `" src="/os/static/js/admin-os-intel.js?v=` + assetVer("js/admin-os-intel.js") + `"></script>`
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

// siteAnalyticsKey is the cache key of v's page for a window, under
// siteAnalyticsPrefix: its scope, "" for the primary.
func siteAnalyticsKey(v analyticsView, days int) string {
	return siteAnalyticsPrefix(v) + strconv.Itoa(days)
}

func siteAnalyticsPrefix(v analyticsView) string { return "site-analytics:" + v.scope + ":" }

// renderSiteAnalyticsBody is v's report: one site's, or every site's on
// Platform. Every reader is given v.scope (a site's id as the ingest wrote
// it, "" for the primary, or every site), so nothing here can count another
// site's traffic. What the page is opened for is laid open (the figures, the
// trend, who is reading now, the pages read and where readers came from);
// every other section is a fold whose head still says what it holds.
func (a *App) renderSiteAnalyticsBody(ctx context.Context, v analyticsView, host string, days int) string {
	st := a.analytics
	site, explore, phrase := v.scope, v.base+"/explore", analyticsPhrase(days)
	title, whose := v.title(), host
	var scopeNote ui.HTML
	if v.all() {
		n, note := a.analyticsScopeNote(ctx)
		if n > 1 {
			whose = strconv.Itoa(n) + " sites"
		}
		scopeNote = note
	}
	exportBtn, exportSheet := osExportSheet(v.base+"/export", days)
	page := ui.OverviewPage{Title: title, Actions: osPeriodSelector(v.base, days) + exploreButton(explore, days) + exportBtn}
	ov, err := st.OverviewSinceScoped(ctx, site, days)
	if err != nil {
		page.State = ui.Text("The figures could not be read: " + err.Error())
		return string(ui.Overview(page, ui.Band{Title: "Visitors"}))
	}
	now := time.Now().UTC()
	prev, _ := st.OverviewBetweenScoped(ctx, site, now.AddDate(0, 0, -(2*days-1)).Format("2006-01-02"), now.AddDate(0, 0, -(days-1)).Format("2006-01-02"))
	series, _ := st.PageviewSeriesScoped(ctx, site, days)
	top, _ := st.TopPagesScoped(ctx, site, days, 10)
	landing, _ := st.LandingPagesScoped(ctx, site, days, 10)
	refs, _ := st.TopReferrersScoped(ctx, site, days, 10)
	channels, _ := st.ChannelsScoped(ctx, site, days)
	audience := func(col string) []analytics.AudienceStat {
		out, _ := st.AudienceScoped(ctx, site, col, days)
		return out
	}
	events, _ := st.CustomEventsScoped(ctx, site, days)
	utm, _ := st.UTMStatsScoped(ctx, site, days)

	page.State = ui.Text("No visits to " + whose + " " + analyticsWhen(days))
	if ov.UniqueVisitors > 0 {
		page.State = ui.Text(countOf(ov.UniqueVisitors, "visitor") + " to " + whose + " " + analyticsWhen(days))
	}

	pageBars := make([]osChartBar, 0, len(top))
	for _, p := range top {
		pageBars = append(pageBars, osChartBar{Label: prettyPathText(p.Path), Value: p.Pageviews})
	}
	landBars := make([]osChartBar, 0, len(landing))
	for _, l := range landing {
		landBars = append(landBars, osChartBar{Label: prettyPathText(l.Path), Value: l.Visits})
	}
	refBars := make([]osChartBar, 0, len(refs))
	refTotal := 0
	for _, r := range refs {
		refBars = append(refBars, osChartBar{Label: r.Referrer, Value: r.Count})
		refTotal += r.Count
	}
	eventBars := make([]osChartBar, 0, len(events))
	eventTotal := 0
	for _, e := range events {
		eventBars = append(eventBars, osChartBar{Label: e.Name, Value: e.Count})
		eventTotal += e.Count
	}
	listed := func(items []analytics.AudienceStat, empty string) string {
		return osBarList(osBarsFromAudience(items), osShareOfListed(), empty)
	}

	var sections []ui.HTML
	if v.all() {
		sections = append(sections, scopeNote, ui.HTML(a.osEachSiteSection(ctx, days)))
	}
	sections = append(sections,
		ui.HTML(analyticsCols(
			string(ui.Section("Top pages", "Page views", ui.HTML(osBarList(pageBars, osShareOf(ov.TotalPageviews), "No page views recorded yet.")))),
			string(ui.Section("Landing pages", "Where visits began", ui.HTML(osBarList(landBars, osShareOf(ov.TotalVisits), "No visits recorded yet.")))))),
		ui.HTML(analyticsCols(
			string(ui.Section("Referrers", "Other sites that sent readers", ui.HTML(osBarList(refBars, osShareOf(refTotal), "No links from other sites yet.")))),
			string(ui.Section("How they found you", "From the referrer alone", ui.HTML(osBarList(osBarsFromAudience(channels), osShareOfListed(), "Visitors are grouped here once they arrive.")))))),
		ui.Fold("What they read on", "Devices, browsers and systems", ui.HTML(analyticsCols(
			analyticsPanel("Devices", listed(audience("device"), "No device recorded yet.")),
			analyticsPanel("Browsers", listed(audience("browser"), "No browser recorded yet.")),
			analyticsPanel("Systems", listed(audience("os"), "No system recorded yet."))))),
		ui.Fold("Where they are", "Country, region and city, coarse only", ui.HTML(osGeoSection(audience("country"), audience("region"), audience("city"), cityCredit()))),
		ui.Fold("Campaigns", countOf(len(utm), "tagged source"), ui.HTML(osCampaigns(utm))),
		ui.Fold("Events", countOf(eventTotal, "custom event"), ui.HTML(osBarList(eventBars, osShareHidden(), "No custom events yet."))),
		ui.HTML(a.osPerformanceSection(ctx, site, days)),
		ui.HTML(a.osSearchesSection(ctx, site, days)),
		ui.HTML(a.osGoalsSectionOf(ctx, v, days)),
		ui.HTML(a.osSegmentsSection(ctx, v, days)),
		ui.HTML(a.osBehaviourSection(ctx, site, explore, days)),
		ui.HTML(a.osContentSection(ctx, site, explore, days)),
		ui.HTML(a.osJourneySection(ctx, site, days)),
		exportSheet,
	)
	return string(ui.Overview(page, ui.Band{Title: "Visitors", Figures: visitorFigures(ov, prev),
		Chart: ui.HTML(osTrendChart(series, "Page views and visitors a day, "+phrase)),
		Aside: ui.Section("Right now", "", ui.HTML(osLiveCard(v.base+"/realtime", cityCredit())))}, sections...))
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
		{Value: strconv.Itoa(ov.UniqueVisitors), Label: "Visitors", Note: analyticsChange(ov.UniqueVisitors, prev.UniqueVisitors), Def: analyticsDefs + "visitor"},
		{Value: strconv.Itoa(ov.TotalPageviews), Label: "Page views", Note: analyticsChange(ov.TotalPageviews, prev.TotalPageviews), Def: analyticsDefs + "page-view"},
	}
	if ov.TotalVisits > 0 {
		figs = append(figs,
			ui.Figure{Value: strconv.Itoa(ov.TotalVisits), Label: "Visits", Note: analyticsChange(ov.TotalVisits, prev.TotalVisits), Def: analyticsDefs + "visit"},
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
