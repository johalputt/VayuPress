// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

// An overview says its kind, carries its state beside the title, and sets its
// one figure group beside what stands with it; tiers and the like follow.
func TestAnOverviewSetsItsFiguresBesideTheirCompanion(t *testing.T) {
	page := string(Overview(OverviewPage{Title: "Members", State: Text("182 members, 14 paying"), Actions: `<a class="btn">Export</a>`},
		Band{Title: "Growth", Hint: "Last 30 days", Figures: []Figure{{Value: "+23", Label: "New members"}}, Chart: `<svg class="chart"></svg>`,
			Aside: Section("Recent", "", "<ul></ul>")},
		Section("Tiers", "", "rows")))
	for _, want := range []string{`data-page-kind="overview"`, `<h1>Members <span class="sa-overview__state">182 members, 14 paying</span></h1>`,
		`<div class="sa-overview__band"><section>`, `>Growth</h2><span class="section-head__hint">Last 30 days</span>`, "New members", `<svg class="chart"></svg></section><section>`,
		`>Tiers</h2>`} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q in\n%s", want, page)
		}
	}
	if strings.Index(page, "Recent") > strings.Index(page, "Tiers") {
		t.Error("the band's companion comes after the rows")
	}
	if strings.Contains(page, "sa-overview__band--full") {
		t.Error("a band with a companion takes the whole width")
	}
	alone := string(Overview(OverviewPage{Title: "Analytics"}, Band{Title: "Visitors"}))
	if !strings.Contains(alone, "sa-overview__band sa-overview__band--full") || strings.Contains(alone, "stat-grid") {
		t.Errorf("a band with no companion and no figures:\n%s", alone)
	}
	if strings.Contains(alone, "sa-overview__state") {
		t.Error("an empty state is drawn")
	}
}

// A band's sentence is escaped text before the figures, and a band without
// one draws no empty paragraph.
func TestABandSaysItsFiguresInASentence(t *testing.T) {
	page := string(Overview(OverviewPage{Title: "SEO"}, Band{Title: "Posts", Sentence: "3 of 4 posts <ready>"}))
	if !strings.Contains(page, `<p class="sa-overview__sentence">3 of 4 posts &lt;ready&gt;</p>`) {
		t.Errorf("the sentence is missing or unescaped:\n%s", page)
	}
	if strings.Contains(string(Overview(OverviewPage{Title: "SEO"}, Band{Title: "Posts"})), "sa-overview__sentence") {
		t.Error("a band without a sentence draws one")
	}
}
