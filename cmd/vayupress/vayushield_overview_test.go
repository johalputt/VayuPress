// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/vayushield"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

// Shield is an overview with its three pages as tabs: the state beside the
// title, what it turned away beside the crawlers it let through, the layers
// as rows, then its reports laid open. Its settings form rises in a sheet, and
// nothing on the page itself is a folded band, a chip, a badge or a field
// (an ⓘ explanation, ui.Explain, is the one fold the grammar keeps).
func TestShieldIsAnOverviewWithItsPagesAsTabs(t *testing.T) {
	openMigratedDB(t)
	a := &App{siteSettings: settings.New(dbpkg.DB), vayuShield: vayushield.New(vayushield.Config{Enabled: true})}
	a.vayuShield.ApplySettings(vayushield.Settings{Enabled: true})
	page := getPage(t, a.handleOSShield, "/os/shield")
	for _, want := range []string{`data-page-kind="overview"`, `<h1>Shield <span class="sa-overview__state">`,
		`<nav class="tabs" aria-label="Shield"><a class="tab" href="/os/shield" aria-current="page">Bot protection</a>`,
		`href="/os/security">Sign-in security</a>`, `href="/os/vayuveil">VayuVeil</a>`,
		">Turned away</h2>", ">Crawlers let through</h2>", ">Layers</h2>", ">Readers and crawlers</h2>",
		">What is enforcing</h2>", ">Network hardening</h2>", ">History</h2>",
		`<span class="settings-row-tag">L0</span>Console lane`, `<span class="settings-row-tag">L7</span>Request inspection`,
		`data-sheet="vs-settings"`, `<dialog class="sa-sheet" id="vs-settings"`, `hx-post="/os/api/shield/settings"`,
		`hx-get="/os/shield/section/aegis"`, `setAttribute('role','status')`,
		`<div class="page-actions"><span id="vs-status" role="status" aria-live="polite"`} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %s", want)
		}
	}
	_, own, _ := strings.Cut(page, `data-page-kind="overview"`)
	own, _, _ = strings.Cut(own, "<dialog")
	for _, not := range []string{"mon-acc", "mon-chip", `class="badge`, "vs-layer", "<input", "<select"} {
		if strings.Contains(own, not) {
			t.Errorf("the page itself carries %q", not)
		}
	}
}

// The state names the worst condition first, says what was turned away when
// the history can be read, and says no reader was challenged only when the
// visitor check has just shown them served. One seed per rule.
func TestShieldStateSaysTheWorstFirstAndOnlyWhatItKnows(t *testing.T) {
	on := vayushield.Settings{Enabled: true}
	for _, c := range []struct {
		name      string
		cur       vayushield.Settings
		stt       vayushield.Status
		trail     bool
		blocks    int64
		readers   bool
		tone, txt string
	}{
		{"calm", on, vayushield.Status{}, true, 3661, true, "ok", "Protecting · 3,661 turned away today, no reader challenged"},
		{"quiet", on, vayushield.Status{}, true, 0, true, "ok", "Protecting · nothing turned away today, no reader challenged"},
		{"no history", on, vayushield.Status{}, false, 9, true, "ok", "Protecting, no reader challenged"},
		{"observing", on, vayushield.Status{ObserveOnly: true, UnderAttack: true}, false, 0, true, "warn", "Observing: nothing is enforced, no reader challenged"},
		{"off", vayushield.Settings{}, vayushield.Status{}, false, 0, true, "warn", "Off, no reader challenged"},
		{"surge", on, vayushield.Status{SurgeActive: true, UnderAttack: true}, false, 0, true, "warn", "Surge: verifying visitors, no reader challenged"},
		{"attack", on, vayushield.Status{UnderAttack: true}, false, 0, true, "danger", "Under attack, no reader challenged"},
		{"readers stopped", on, vayushield.Status{}, false, 0, false, "danger", "Protecting, ordinary visitors are being challenged"},
	} {
		tone, txt := shieldState(c.cur, c.stt, c.trail, c.blocks, c.readers)
		if tone != c.tone || txt != c.txt {
			t.Errorf("%s: %s %q, want %s %q", c.name, tone, txt, c.tone, c.txt)
		}
	}
}

// The chart is one point a day, oldest first, with a day nothing was recorded
// drawn as zero; the day's figures count the last 24 hours only.
func TestShieldTrailDaysFillsTheGapsAndCountsTheLastDay(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 30, 0, 0, time.UTC)
	tr := botdb.Trail{Hours: []botdb.HourBucket{
		{Hour: "2026-09-29 08:00", Blocks: 5, Challenges: 1},
		{Hour: "2026-10-01 11:00", Blocks: 7, Challenges: 2}, // older than a day
		{Hour: "2026-10-01 13:00", Blocks: 3, Challenges: 4},
		{Hour: "2026-10-02 09:00", Blocks: 2},
	}}
	daily, blocks, challenges := shieldTrailDays(tr, 4, now)
	if want := []int{5, 0, 10, 2}; len(daily) != 4 || daily[0] != want[0] || daily[1] != want[1] || daily[2] != want[2] || daily[3] != want[3] {
		t.Errorf("daily %v, want %v", daily, want)
	}
	if blocks != 5 || challenges != 4 {
		t.Errorf("last day: %d blocks, %d challenges; want 5 and 4", blocks, challenges)
	}
}

// Sign-in security and VayuVeil open on the same title and tabs, each with its
// own tab current: the three pages read as one place, and no sidebar repeats
// the tabs.
func TestShieldsOtherTabsShareItsTitleAndTabs(t *testing.T) {
	openMigratedDB(t)
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	sec := getPage(t, a.handleOSSecurity, "/os/security")
	for _, want := range []string{`<div class="page-header"><h1>Shield</h1></div><nav class="tabs" aria-label="Shield">`,
		`<a class="tab" href="/os/security" aria-current="page">Sign-in security</a>`} {
		if !strings.Contains(sec, want) {
			t.Errorf("Sign-in security: missing %s", want)
		}
	}
	if strings.Contains(sec, `class="sa-appside"`) {
		t.Error("Shield still draws a sidebar beside its tabs")
	}
	veil := string(saTabsFor(nil, "shield", "/os/vayuveil"))
	if !strings.Contains(veil, `<a class="tab" href="/os/vayuveil" aria-current="page">VayuVeil</a>`) || strings.Count(veil, "aria-current") != 1 {
		t.Errorf("VayuVeil's tabs: %s", veil)
	}
}

// The settings' descriptions are text, escaped once. Two were written with a
// <strong> and HTML entities, so the sheet printed "<strong>While this is
// on…" and "&ldquo;Just a moment&rdquo;" to the operator.
func TestShieldSettingsPrintNoEscapedMarkup(t *testing.T) {
	openMigratedDB(t)
	a := &App{siteSettings: settings.New(dbpkg.DB), vayuShield: vayushield.New(vayushield.Config{Enabled: true})}
	body := a.shieldProtectionBody(t.Context(), false)
	for _, bad := range []string{"&lt;strong", "&lt;/strong", "&amp;ldquo;", "&amp;rdquo;", "&amp;rsquo;", "&amp;mdash;", "&amp;amp;"} {
		if strings.Contains(body, bad) {
			t.Errorf("the settings print %q to the operator", bad)
		}
	}
}
