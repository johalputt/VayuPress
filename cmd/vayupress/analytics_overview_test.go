// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// Analytics is an overview: the range as a segmented control, one chart
// beside who is on the site now, then its sections laid open, none of them a
// disclosure. The one form on it, a new goal, rises in a sheet.
func TestAnalyticsIsAnOverviewWithItsSectionsLaidOpen(t *testing.T) {
	openMigratedDB(t)
	a := &App{analytics: analytics.New(dbpkg.DB)}
	page := a.renderAnalyticsBody(context.Background(), 30, "30 days")
	if page == "" {
		t.Fatal("the report did not render")
	}
	for _, want := range []string{`data-page-kind="overview"`, `<div data-period><nav class="seg-filter" aria-label="Period">`,
		`is-active" aria-current="page" href="/os/analytics?days=30">30 days`, "data-live-count",
		">Top pages<", ">Referrers<", ">Where they are<", ">Goals<", ">Journeys<", ">Export<",
		`data-sheet="goal-new"`, `<dialog class="sa-sheet" id="goal-new"`} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	own, _, _ := strings.Cut(page, "<dialog")
	for _, not := range []string{"mon-acc", `class="card`, `class="badge`, "<form", "<select", "vm-delta", "style="} {
		if strings.Contains(own, not) {
			t.Errorf("the page itself carries %q", not)
		}
	}
}
