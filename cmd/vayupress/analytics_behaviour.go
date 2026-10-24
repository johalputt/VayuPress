// SPDX-License-Identifier: Apache-2.0

package main

// analytics_behaviour.go — what readers did on the pages they read: where
// their visits ended, the links they followed off the site, the files they
// fetched, and how far down each page they read. The first three are the
// Explorer's own figures, so each has one definition and opens there in
// full; the depth is the engagement beacon's.

import (
	"context"
	"html"
	"strconv"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/ui"
	vastore "github.com/johalputt/vayupress/internal/vayuanalytics/store"
)

// behaviourShown is how many values each part lists; the Explorer has the
// rest.
const behaviourShown = 10

// osBehaviourSection is the Analytics page's section on what readers did,
// for scope (a site, or analytics.ScopeAllDomains), linking into the
// Explorer at explore.
func (a *App) osBehaviourSection(ctx context.Context, scope, explore string, days int) string {
	part := func(title string, m analytics.ExploreMetric, by, empty string) string {
		res, err := a.analytics.Explore(ctx, analytics.ExploreQuery{Scope: scope, Metric: m, By: by, Days: days})
		if err != nil {
			return analyticsPanel(title, string(ui.Callout("danger", ui.Text("Not counted: "+err.Error()))))
		}
		bars := make([]osChartBar, 0, behaviourShown)
		for i, r := range res.Rows {
			if i == behaviourShown {
				break
			}
			bars = append(bars, osChartBar{Label: prettyPathText(r.Key), Value: int(r.Value)})
		}
		more := ""
		if len(res.Rows) > behaviourShown {
			more = `<p class="settings-row-hint"><a href="` + html.EscapeString(explore+"?metric="+string(m)+"&by="+by+"&days="+strconv.Itoa(days)) + `">All of them in the Explorer</a></p>`
		}
		return analyticsPanel(title, osBarList(bars, osShareOf(int(res.Total)), empty)+more)
	}
	return string(ui.Section("What readers did", "", ui.HTML(analyticsCols(
		part("Where visits ended", analytics.ExploreVisits, "exit", "No visit recorded yet."),
		part("Links out", analytics.ExploreOutbound, "event", "No link off the site followed yet."),
		part("Downloads", analytics.ExploreDownloads, "event", "No file fetched yet."),
	)+a.readingDepth(ctx, scope, days))))
}

// readingDepth is how far down the most read pages their readers scrolled.
func (a *App) readingDepth(ctx context.Context, scope string, days int) string {
	if a.vaEngagement == nil {
		return analyticsPanel("How far they read", `<p class="table-empty">Depth is measured by the reading beacon, which is not running.</p>`)
	}
	site := scope
	if scope == analytics.ScopeAllDomains {
		site = vastore.AllDomains
	}
	pages, err := a.vaEngagement.ReadingDepth(ctx, site, days, behaviourShown)
	if err != nil {
		return analyticsPanel("How far they read", string(ui.Callout("danger", ui.Text("Not counted: "+err.Error()))))
	}
	rows := make([][]ui.HTML, 0, len(pages))
	for _, p := range pages {
		rows = append(rows, []ui.HTML{ui.Text(prettyPathText(p.Path)), ui.Text(strconv.FormatInt(p.Readings, 10)),
			ui.HTML(osShareLabel(int(p.ReachedHalf), int(p.Readings))), ui.HTML(osShareLabel(int(p.ReachedEnd), int(p.Readings)))})
	}
	return analyticsPanel("How far they read", `<p class="settings-row-hint">Readings that reported their depth as the reader left.</p>`+
		string(ui.Table([]string{"Page", "Readings", "Past half", "Past 90%"}, rows, "No reading has reported its depth yet.")))
}
