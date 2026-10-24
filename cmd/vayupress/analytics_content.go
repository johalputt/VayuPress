// SPDX-License-Identifier: Apache-2.0

package main

// analytics_content.go — what each page did, on the Analytics pages and in
// the API: the categories of analytics.ClassifyContent, each page with the
// figures and thresholds that put it there.

import (
	"context"
	"html"
	"net/http"
	"strconv"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/ui"
	vastore "github.com/johalputt/vayupress/internal/vayuanalytics/store"
)

// contentShownOnPage is how many pages each category lists on the page; the
// API lists analytics.ContentShown.
const contentShownOnPage = 5

// contentReport is scope's categories over days. The readings come from the
// reading beacon's own table and are merged into the views by the page's
// path as analytics records it: rows written before the beacon dropped a
// trailing slash are the same page's.
func (a *App) contentReport(ctx context.Context, scope string, days int) (analytics.ContentReport, error) {
	pages, err := a.analytics.ContentPagesScoped(ctx, scope, days)
	if err != nil {
		return analytics.ContentReport{}, err
	}
	if a.vaEngagement != nil {
		site := scope
		if scope == analytics.ScopeAllDomains {
			site = vastore.AllDomains
		}
		read, err := a.vaEngagement.PageEngagement(ctx, site, days)
		if err != nil {
			return analytics.ContentReport{}, err
		}
		at := make(map[string]int, len(pages))
		for i, p := range pages {
			at[p.Path] = i
		}
		for _, r := range read {
			// A page read but not viewed in the range has no views to set
			// its readings against.
			if i, ok := at[analytics.NormalizePath(r.Path)]; ok {
				pages[i].Readings += r.Readings
				pages[i].Engaged += r.Engaged
			}
		}
	}
	return analytics.ClassifyContent(pages), nil
}

// osContentSection is the Analytics page's section on what each page did.
func (a *App) osContentSection(ctx context.Context, scope, explore string, days int) string {
	const title = "What the content did"
	rep, err := a.contentReport(ctx, scope, days)
	if err != nil {
		return string(ui.Fold(title, "", ui.Callout("danger", ui.Text("Not counted: "+err.Error()))))
	}
	listed := false
	for _, c := range rep.Categories {
		listed = listed || c.Total > 0
	}
	if !listed {
		return string(ui.Fold(title, "No page has met a category yet", ui.HTML(`<p class="table-empty">No page has met a category yet.</p>`)))
	}
	panels := make([]string, 0, len(rep.Categories))
	var why string
	for _, c := range rep.Categories {
		line := `<p><strong>` + html.EscapeString(c.Label) + `.</strong> ` + html.EscapeString(c.Rule)
		if c.NotCompared != "" {
			why += line + ` Not compared: ` + html.EscapeString(c.NotCompared) + `</p>`
			panels = append(panels, analyticsPanel(c.Label, `<p class="table-empty">Too few pages to compare yet.</p>`))
			continue
		}
		why += line + `</p>`
		rows := make([][]ui.HTML, 0, contentShownOnPage)
		for i, p := range c.Pages {
			if i == contentShownOnPage {
				break
			}
			rows = append(rows, []ui.HTML{ui.Text(prettyPathText(p.Path)), ui.Text(p.Reason)})
		}
		body := string(ui.Table([]string{"Page", "Why it is here"}, rows, "No page met this."))
		if c.Total > contentShownOnPage {
			body += `<p class="settings-row-hint">` + strconv.Itoa(contentShownOnPage) + ` of ` + strconv.Itoa(c.Total) +
				`. <a href="` + html.EscapeString(explore+"?metric="+string(analytics.ExploreViews)+"&by=path&compare=1&days="+strconv.Itoa(days)) +
				`">Every page in the Explorer</a></p>`
		}
		panels = append(panels, analyticsPanel(c.Label, body))
	}
	why += `<p>No page is called good or bad: each category is a measured rule, and each page says the figures that put it there. ` +
		strconv.Itoa(rep.Compared) + ` pages had views enough to compare, and ` + strconv.Itoa(rep.Engagement) + ` of them readings enough as well.</p>`
	return string(ui.Fold(title, "Pages by what was measured of them over the period",
		ui.HTML(`<div class="sa-editorial">`+analyticsCols(panels...)+`</div>`+string(ui.Explain(ui.HTML(why))))))
}

func (a *App) handleSiteAnalyticsContent(w http.ResponseWriter, r *http.Request) {
	sc, days := siteScopeOf(r), siteDays(r)
	rep, err := a.contentReport(r.Context(), sc.scope, days)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, struct {
		siteHead
		analytics.ContentReport
	}{headOf(sc, days), rep})
}
