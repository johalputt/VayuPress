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
)

var (
	openSectionTitle = regexp.MustCompile(`<section><div class="section-head"><h2 class="section-head__title">([^<]+)</h2>`)
	foldTitle        = regexp.MustCompile(`<details class="sa-fold"><summary class="section-head"><h2 class="section-head__title">([^<]+)</h2>`)
)

// Analytics opens what it is opened for and folds the rest. Laid open: the
// figures and the trend, who is reading now, the pages read and where the
// readers came from. Every other section is a fold, read closed, its head
// still saying what it holds. The three pages are one renderer, so the rule
// is the same on each, with every site's Each site above them. The one form
// on the page itself is none: a new goal and an export rise in sheets.
func TestAnalyticsOpensWhatItIsForAndFoldsTheRest(t *testing.T) {
	openMigratedDB(t)
	a := &App{analytics: analytics.New(dbpkg.DB)}
	open := []string{"Top pages", "Landing pages", "Referrers", "How they found you"}
	folded := []string{"What they read on", "Where they are", "Campaigns", "Events", "Performance",
		"Searches", "Goals", "Segments", "What readers did", "What the content did", "Journeys"}
	for _, tc := range []struct {
		v      analyticsView
		host   string
		opened []string
	}{
		{primaryView, "example.test", open},
		{hostedView(domain.Domain{ID: "s1"}), "one.test", open},
		{platformView, "every site", append([]string{"Each site"}, open...)},
	} {
		page := a.renderSiteAnalyticsBody(context.Background(), tc.v, tc.host, 30)
		var opened, folds []string
		for _, m := range openSectionTitle.FindAllStringSubmatch(page, -1) {
			opened = append(opened, m[1])
		}
		for _, m := range foldTitle.FindAllStringSubmatch(page, -1) {
			folds = append(folds, m[1])
		}
		// The band (its figures and trend) and its aside, Right now, come first.
		if want := strings.Join(append([]string{"Visitors", "Right now"}, tc.opened...), ", "); strings.Join(opened, ", ") != want {
			t.Errorf("%s: open sections %q, want %q", tc.v.base, strings.Join(opened, ", "), want)
		}
		if strings.Join(folds, ", ") != strings.Join(folded, ", ") {
			t.Errorf("%s: folds %q, want %q", tc.v.base, strings.Join(folds, ", "), strings.Join(folded, ", "))
		}
		if strings.Contains(page, `<details class="sa-fold" open`) {
			t.Errorf("%s: a fold is opened on arrival", tc.v.base)
		}
		for _, want := range []string{`data-page-kind="overview"`, `<div data-period><nav class="seg-filter" aria-label="Period">`,
			`is-active" aria-current="page" href="` + tc.v.base + `?days=30">30 days`, "data-live-count",
			`data-sheet="goal-new"`, `<dialog class="sa-sheet" id="goal-new"`, `<dialog class="sa-sheet" id="analytics-export"`} {
			if !strings.Contains(page, want) {
				t.Errorf("%s: missing %q", tc.v.base, want)
			}
		}
		own, _, _ := strings.Cut(page, "<dialog")
		for _, not := range []string{"mon-acc", `class="card`, `class="badge`, "<form", "<select", "vm-delta", "style="} {
			if strings.Contains(own, not) {
				t.Errorf("%s: the page itself carries %q", tc.v.base, not)
			}
		}
	}
}
