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

// handleOSScopedAnalytics renders one site's Analytics: the install's page,
// fed by the readers that take a site, so a client reads their own site with
// the same figures, definitions and charts the operator reads for the
// install.
func (a *App) handleOSScopedAnalytics(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	d, ok := osScopedDomain(r)
	if !ok {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	title := "Visitors · " + d.Host
	if a.analytics == nil {
		writeOSHTML(w, r, adminOSLayout(nonce, title, "optimize", cfg, ui.Overview(ui.OverviewPage{Title: "Visitors"},
			ui.Band{Title: "Visitors", Aside: ui.Empty("trend", "Analytics are off", "This install was started without the analytics store.", "")})))
		return
	}
	days, label := analyticsPeriod(r)
	// Counted off the request, in the cache the install's Analytics uses: an
	// exact count of a busy site's visitors over a month reads every view in
	// it (0.3 s at a million events, 1.1 s for the whole install), and no
	// console page waits on that.
	id, host := d.ID, d.Host
	body, ready := adminDash.get("site-analytics:"+id+":"+strconv.Itoa(days), analyticsFragmentTTL, func(ctx context.Context) string {
		return a.renderSiteAnalyticsBody(ctx, id, host, days, label)
	})
	if !ready {
		body = string(ui.Overview(ui.OverviewPage{Title: "Visitors", State: ui.Text("Counting the " + label + " for " + host),
			Actions: osPeriodSelector("/os/d/"+id+"/analytics", days)},
			ui.Band{Title: "Visitors", Chart: `<p class="table-empty">This is counted in the background; reload in a few seconds.</p>`}))
	}
	// The setting is read off the request, not the cache, so a save shows at
	// once.
	body += a.siteRetentionSection(r.Context(), d, nonce)
	body += a.alertsSection(r.Context(), d.ID, "/os/d/"+d.ID+"/api/analytics/alerts", cfg.AccessLevel >= accessAdmin, nonce)
	// The live panel's poller; the body above is cached, the script is not.
	body += `<script nonce="` + nonce + `" src="/os/static/js/admin-os-intel.js?v=` + assetVer("js/admin-os-intel.js") + `"></script>`
	writeOSHTML(w, r, adminOSLayout(nonce, title, "optimize", cfg, htmpl.HTML(body)))
}

// renderSiteAnalyticsBody is one site's report. Every reader is given the
// site (d.ID, the value the ingest wrote), so nothing here can count another
// site's traffic.
func (a *App) renderSiteAnalyticsBody(ctx context.Context, site, host string, days int, label string) string {
	st := a.analytics
	page := ui.OverviewPage{Title: "Visitors", Actions: osPeriodSelector("/os/d/"+site+"/analytics", days) + exploreButton("/os/d/"+site+"/analytics/explore", days)}
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
		ui.Section("Speed for readers", "Core Web Vitals, 75th percentile", ui.HTML(a.siteVitals(ctx, site, days))),
		ui.Section("Events", "What this site tracks with data-vp-event or VayuPress.track()",
			ui.HTML(bars(eventBars, osShareHidden(), "No custom events yet."))),
		ui.HTML(a.osSearchesSection(ctx, site, days)),
		ui.HTML(a.osGoalsSectionScoped(ctx, site, "/os/d/"+site+"/analytics/goals", days)),
		ui.Explain(ui.HTML(`<p>These are this site's own figures: every count reads only the visits recorded for ` + esc(host) +
			`. A visit is a reader's pages with no 30-minute gap; a visitor is recognised within a day by a salted hash that changes daily, so "visitors" over a week counts a person once for each day they came. Nothing here uses a cookie, and nothing leaves this server. <a href="/docs/ANALYTICS" target="_blank" rel="noopener">What each figure means</a>.</p>`)),
	}
	return string(ui.Overview(page, ui.Band{Title: "Visitors", Hint: "The " + label, Figures: figs, Chart: ui.HTML(chart),
		Aside: ui.Section("Right now", "", ui.HTML(osLiveCard("/os/d/"+site+"/analytics/realtime")))}, sections...))
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

// siteVitals is the site's Core Web Vitals, each against Google's "good"
// line, from the pages' own measurements (the engagement beacon).
func (a *App) siteVitals(ctx context.Context, site string, days int) string {
	if a.vaEngagement == nil {
		return `<p class="table-empty">Speed is measured by the reading beacon, which is not running.</p>`
	}
	v, err := a.vaEngagement.WebVitalsP75Scoped(ctx, site, days)
	if err != nil || v == nil || v.Samples == 0 {
		return `<p class="table-empty">No page has reported its speed yet.</p>`
	}
	row := func(name, value string, good bool, line string) string {
		tone := "ok"
		if !good {
			tone = "warn"
		}
		return `<tr><td>` + name + `</td><td class="mono">` + value + `</td><td>` + string(ui.State(tone, map[bool]string{true: "Good", false: "Needs work"}[good])) + `</td><td class="muted text-sm">` + line + `</td></tr>`
	}
	return `<div class="table-wrap"><table class="table"><thead><tr><th>Measure</th><th>p75</th><th></th><th>Good is</th></tr></thead><tbody>` +
		row("Largest paint (LCP)", strconv.Itoa(v.P75LCPMs)+" ms", v.P75LCPMs <= 2500, "2.5 s or less") +
		row("Response to input (INP)", strconv.Itoa(v.P75INPMs)+" ms", v.P75INPMs <= 200, "200 ms or less") +
		row("Layout shift (CLS)", strconv.FormatFloat(float64(v.P75CLSX100)/100, 'f', 2, 64), v.P75CLSX100 <= 10, "0.10 or less") +
		`</tbody></table></div><p class="settings-row-hint">From ` + strconv.FormatInt(v.Samples, 10) + ` page loads that reported.</p>`
}
