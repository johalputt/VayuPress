// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/ui"
)

var (
	openSectionTitle = regexp.MustCompile(`<section><div class="section-head"><h2 class="section-head__title">([^<]+)</h2>`)
	dataPart         = regexp.MustCompile(`data-part="([a-z-]+)"`)
)

// Analytics is a bar of views, each its own page with its sections open:
// thirteen folds on one page had no way to reach a part but past the rest.
// Every part the page had is on one view (the inventory below, which is the
// promise that the redesign took nothing away), on the install's page, a
// hosted site's and every site's, which are one renderer. The bar names
// every view and marks the one shown; the period is one menu that keeps the
// view; the page itself carries no form, a new goal and an export rising in
// sheets.
func TestEveryAnalyticsViewHoldsItsParts(t *testing.T) {
	openMigratedDB(t)
	a := &App{analytics: analytics.New(dbpkg.DB)}
	type want struct{ sections, parts string }
	views := map[string]want{
		"overview":    {"Visitors, Every day, Right now, Top pages, How they found you", "trend, now, top-pages, channels"},
		"audience":    {"What they read on, Where they are", "devices, location"},
		"sources":     {"Referrers, How they found you, Campaigns", "referrers, channels, campaigns"},
		"content":     {"Top pages, Landing pages, What the content did, Searches", "top-pages, landing, content, searches"},
		"behaviour":   {"What readers did, Journeys, Events", "behaviour, journeys, events"},
		"performance": {"Performance", "performance"},
		"goals":       {"Goals, Segments", "goals, segments"},
		"live":        {"Right now", "live"},
	}
	if len(views) != len(analyticsViews) {
		t.Fatalf("%d views listed here, %d on the bar", len(views), len(analyticsViews))
	}
	for _, tc := range []struct {
		v    analyticsView
		host string
	}{
		{primaryView, "example.test"},
		{hostedView(domain.Domain{ID: "s1"}), "one.test"},
		{platformView, "every site"},
	} {
		for _, view := range analyticsViews {
			w := views[view.id]
			if tc.v.all() && view.id == "overview" {
				w = want{"Visitors, Every day, Right now, Each site, Top pages, How they found you", "trend, now, each-site, top-pages, channels"}
			}
			page := a.renderSiteAnalyticsBody(context.Background(), tc.v, tc.host, 30, view.id)
			var sections, parts []string
			for _, m := range openSectionTitle.FindAllStringSubmatch(page, -1) {
				sections = append(sections, m[1])
			}
			for _, m := range dataPart.FindAllStringSubmatch(page, -1) {
				parts = append(parts, m[1])
			}
			if got := strings.Join(sections, ", "); got != w.sections {
				t.Errorf("%s %s: sections %q, want %q", tc.v.base, view.id, got, w.sections)
			}
			if got := strings.Join(parts, ", "); got != w.parts {
				t.Errorf("%s %s: parts %q, want %q", tc.v.base, view.id, got, w.parts)
			}
			if strings.Contains(page, "sa-fold") {
				t.Errorf("%s %s: a section is folded", tc.v.base, view.id)
			}
			for _, v := range analyticsViews {
				link := `<a class="tab" href="` + string(ui.Text(analyticsHref(tc.v.base, v.id, analyticsSpan{days: 30}))) + `"`
				if v.id == view.id {
					link += ` aria-current="page"`
				}
				if !strings.Contains(page, link+`>`+v.label+`</a>`) {
					t.Errorf("%s %s: the bar has no %q link as %s", tc.v.base, view.id, v.label, link)
				}
			}
			for _, want := range []string{`data-page-kind="overview"`, `<details class="sa-pop" data-period><summary class="btn btn--sm">Last 30 days</summary>`,
				`<dialog class="sa-sheet" id="analytics-export"`} {
				if !strings.Contains(page, want) {
					t.Errorf("%s %s: missing %q", tc.v.base, view.id, want)
				}
			}
			own, _, _ := strings.Cut(page, "<dialog")
			for _, not := range []string{"mon-acc", `class="card`, `class="badge`, "<form", "<select", "vm-delta", "style="} {
				if strings.Contains(own, not) {
					t.Errorf("%s %s: the page itself carries %q", tc.v.base, view.id, not)
				}
			}
		}
		if goals := a.renderSiteAnalyticsBody(context.Background(), tc.v, tc.host, 30, "goals"); !strings.Contains(goals, `data-sheet="goal-new"`) || !strings.Contains(goals, `<dialog class="sa-sheet" id="goal-new"`) {
			t.Errorf("%s: Goals has no New goal sheet", tc.v.base)
		}
		if p := a.renderSiteAnalyticsBody(context.Background(), tc.v, tc.host, 7, "audience"); !strings.Contains(p, `href="`+string(ui.Text(tc.v.base+"?days=7&view=audience"))+`" aria-current="page">Audience`) ||
			!strings.Contains(p, `href="`+string(ui.Text(tc.v.base+"?view=audience"))+`"`) {
			t.Errorf("%s: a view's bar and period menu do not keep the view and the period", tc.v.base)
		}
	}
}
