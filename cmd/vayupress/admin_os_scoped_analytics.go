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
		writeOSHTML(w, r, adminOSLayout(nonce, title, app, cfg, ui.Overview(ui.OverviewPage{Title: "Visitors"},
			ui.Band{Title: "Visitors", Aside: ui.Empty("trend", "Analytics are off", "This install was started without the analytics store.", "")})))
		return
	}
	days, label := analyticsPeriod(r)
	// Counted off the request, in the cache the install's Analytics uses: an
	// exact count of a busy site's visitors over a month reads every view in
	// it (0.3 s at a million events, 1.1 s for the whole install), and no
	// console page waits on that.
	body, ready := adminDash.get(siteAnalyticsKey(v, days), analyticsFragmentTTL, func(ctx context.Context) string {
		return a.renderSiteAnalyticsBody(ctx, v, host, days, label)
	})
	if !ready {
		body = string(ui.Overview(ui.OverviewPage{Title: "Visitors", State: ui.Text("Counting the " + label + " for " + host),
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
	// The live panel's poller; the body above is cached, the script is not.
	body += `<script nonce="` + nonce + `" src="/os/static/js/admin-os-intel.js?v=` + assetVer("js/admin-os-intel.js") + `"></script>`
	writeOSHTML(w, r, adminOSLayout(nonce, title, app, cfg, htmpl.HTML(body)))
}

// siteAnalyticsKey is the cache key of v's page for a window, under
// siteAnalyticsPrefix: its scope, "" for the primary.
func siteAnalyticsKey(v analyticsView, days int) string {
	return siteAnalyticsPrefix(v) + strconv.Itoa(days)
}

func siteAnalyticsPrefix(v analyticsView) string { return "site-analytics:" + v.scope + ":" }

// renderSiteAnalyticsBody is one site's report. Every reader is given the
// site (v.scope, the value the ingest wrote), so nothing here can count
// another site's traffic.
func (a *App) renderSiteAnalyticsBody(ctx context.Context, v analyticsView, host string, days int, label string) string {
	st := a.analytics
	site, explore := v.scope, v.base+"/explore"
	// The primary's page is the rail's Analytics and keeps its name; a
	// hosted site's is its Visitors.
	title := "Visitors"
	if !v.hosted {
		title = "Analytics"
	}
	page := ui.OverviewPage{Title: title, Actions: osPeriodSelector(v.base, days) + exploreButton(explore, days)}
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

	page.State = ui.Text("No visits to " + host + " in the " + label)
	if ov.UniqueVisitors > 0 {
		page.State = ui.Text(countOf(ov.UniqueVisitors, "visitor") + " to " + host + " in the " + label)
	}
	figs := append(analyticsFigures(ov, prev, 0), siteVisitFigures(ov, prev)...)
	chart := `<p class="table-empty">Visits to this site appear here as people read it.</p>`
	if len(series) > 0 {
		chart = osTrendChart(series, "Traffic over the "+label)
	}

	bars := func(items []osChartBar, denom osBarDenom, empty string) string { return osBarList(items, denom, empty) }
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
	for _, e := range events {
		eventBars = append(eventBars, osChartBar{Label: e.Name, Value: e.Count})
	}

	sections := []ui.HTML{
		ui.HTML(analyticsCols(
			string(ui.Section("Top pages", "Page views", ui.HTML(bars(pageBars, osShareOf(ov.TotalPageviews), "No page views recorded yet.")))),
			string(ui.Section("Landing pages", "Where visits began", ui.HTML(bars(landBars, osShareOf(ov.TotalVisits), "No visits recorded yet.")))))),
		ui.HTML(analyticsCols(
			string(ui.Section("Referrers", "Other sites that sent readers", ui.HTML(bars(refBars, osShareOf(refTotal), "No links from other sites yet.")))),
			string(ui.Section("How they found you", "From the referrer alone", ui.HTML(bars(osBarsFromAudience(channels), osShareOfListed(), "Visitors are grouped here once they arrive.")))))),
		ui.Section("What they read on", "", ui.HTML(analyticsCols(
			analyticsPanel("Devices", osDonut(osSegsFromAudience(audience("device")), "No device data yet.")),
			analyticsPanel("Browsers", bars(osBarsFromAudience(audience("browser")), osShareOfListed(), "No browser data yet.")),
			analyticsPanel("Systems", bars(osBarsFromAudience(audience("os")), osShareOfListed(), "No system data yet."))))),
		ui.Section("Where they are", "Coarse location only", ui.HTML(osGeoSection(audience("country"), audience("region"), audience("city")))),
		ui.Section("Campaigns", "", ui.HTML(osCampaigns(utm))),
		ui.HTML(a.osPerformanceSection(ctx, site, days)),
		ui.Section("Events", "What this site tracks with data-vp-event or VayuPress.track()",
			ui.HTML(bars(eventBars, osShareHidden(), "No custom events yet."))),
		ui.HTML(a.osSearchesSection(ctx, site, days)),
		ui.HTML(a.osGoalsSectionOf(ctx, v, days)),
		ui.HTML(a.osSegmentsSection(ctx, v, days)),
		ui.HTML(a.osBehaviourSection(ctx, site, explore, days)),
		ui.HTML(a.osContentSection(ctx, site, explore, days)),
		ui.HTML(a.osJourneySection(ctx, site, days)),
		ui.HTML(osExportSection(v.base+"/export", days)),
		ui.Explain(ui.HTML(`<p>These are this site's own figures: every count reads only the visits recorded for ` + esc(host) +
			`. A visit is a reader's pages with no 30-minute gap; a visitor is recognised within a day by a salted hash that changes daily, so "visitors" over a week counts a person once for each day they came. Nothing here uses a cookie, and nothing leaves this server. <a href="/docs/ANALYTICS" target="_blank" rel="noopener">What each figure means</a>.</p>`)),
	}
	return string(ui.Overview(page, ui.Band{Title: "Visitors", Hint: "The " + label, Figures: figs, Chart: ui.HTML(chart),
		Aside: ui.Section("Right now", "", ui.HTML(osLiveCard(v.base+"/realtime")))}, sections...))
}

// siteVisitFigures adds what a site's owner asks first after "how many":
// visits, how many stopped at one page, and how long the others stayed.
func siteVisitFigures(ov, prev *analytics.Overview) []ui.Figure {
	if ov == nil || ov.TotalVisits == 0 {
		return nil
	}
	pv := 0
	if prev != nil {
		pv = prev.TotalVisits
	}
	figs := []ui.Figure{
		{Value: strconv.Itoa(ov.TotalVisits), Label: "Visits", Note: analyticsChange(ov.TotalVisits, pv)},
		{Value: strconv.FormatFloat(ov.BounceRate, 'f', 0, 64) + "%", Label: "Bounce rate", Note: "Visits of one page and nothing else"},
	}
	if ov.AvgDuration > 0 {
		figs = append(figs, ui.Figure{Value: osDuration(ov.AvgDuration), Label: "Average visit", Note: "Over the visits of more than one page"})
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
