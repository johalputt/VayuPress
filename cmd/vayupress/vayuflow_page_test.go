// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayuflow"
	"github.com/johalputt/vayupress/internal/vayuflow/flowaudit"
)

func samplePageFlow() vayuflow.Flow {
	return vayuflow.Flow{
		ID: "flow-1", Name: "Weekly digest", Enabled: true, Version: 3,
		Trigger: vayuflow.Trigger{Kind: vayuflow.TriggerSchedule, Cron: "0 9 * * 1"},
		Steps: []vayuflow.Step{{Action: "content.draft.create",
			Params: map[string]string{"title": "Digest", "slug": "digest"}}},
		Budget: vayuflow.Budget{MaxStepsPerRun: 4, MaxRunsPerHour: 2, MaxWritesPerRun: 1,
			MaxEgressPerRun: 1, Timeout: 30 * time.Second},
		Owner: "user-1", Mode: vayuflow.RunDryRun,
	}
}

// renderPage is what every assertion below reads. Rendering and reading the
// OUTPUT — never the handler's source — is the rule here: a source test fails
// an honest refactor and passes a regression that deletes the line from the
// page and leaves it in a comment.
func renderPage(t *testing.T, flows []vayuflow.Flow, rejected map[string]error,
	stats vayuflow.Stats, runs []vayuflow.Run, wired bool) string {
	t.Helper()
	return vayuFlowPage(flows, rejected, stats, runs, flowaudit.Run(flowaudit.Inputs{Wired: wired, Flows: flows, Rejected: rejected}), wired)
}

// The blast radius must read while an accordion is COLLAPSED, because the
// decision an operator makes on this page is whether to arm — and they make it
// from the summary line.
func TestAFlowsBlastRadiusIsVisibleOnThePage(t *testing.T) {
	page := renderPage(t, []vayuflow.Flow{samplePageFlow()}, nil, vayuflow.Stats{}, nil, true)
	for _, want := range []string{
		"Weekly digest",        // name
		"schedule · 0 9 * * 1", // what fires it
		"v3",                   // the version a run will be recorded against
		"draft",                // the highest write it may perform
		"editor",               // the authority its owner must still hold
		"content.draft.create",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not show %q", want)
		}
	}
	// Ceilings, so "bounded" is a number rather than a claim.
	for _, want := range []string{"4 steps", "1 writes", "1 fetches", "2/hour", "30s wall clock"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not show the ceiling %q", want)
		}
	}
}

// A dry-run flow must not read as armed, and a live one must not read as safe.
func TestTheModeChipDistinguishesDryRunFromLive(t *testing.T) {
	dry := samplePageFlow()
	if got := flowModeChip(dry); !strings.Contains(got, "dry-run") || strings.Contains(got, "mon-chip--on") {
		t.Errorf("a dry-run flow rendered as %q; it must not read as armed", got)
	}
	live := samplePageFlow()
	live.Mode = vayuflow.RunLive
	if got := flowModeChip(live); !strings.Contains(got, "mon-chip--on") || !strings.Contains(got, "live") {
		t.Errorf("a live flow rendered as %q", got)
	}
	off := samplePageFlow()
	off.Enabled = false
	if got := flowModeChip(off); !strings.Contains(got, "disabled") {
		t.Errorf("a disabled flow rendered as %q", got)
	}
}

// The dry-run diff is the whole point of dry-run: it must show what the run
// WOULD have done, not merely that it did nothing.
func TestTheDryRunDiffIsRendered(t *testing.T) {
	runs := []vayuflow.Run{{
		ID: "run-1", FlowID: "flow-1", FlowVersion: 3, Mode: vayuflow.RunDryRun,
		Status: vayuflow.StatusSucceeded, Owner: "user-1", OwnerRole: "admin",
		Budget: samplePageFlow().Budget,
		Spend:  vayuflow.Spend{Steps: 1, Writes: 1},
		Steps: []vayuflow.StepRecord{{
			Action:  "content.draft.create",
			Refused: "would write: create draft digest — Weekly digest",
		}},
		StartedAt: time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC),
	}}
	page := renderPage(t, nil, nil, vayuflow.Stats{Runs: 1}, runs, true)
	if !strings.Contains(page, "would write: create draft digest") {
		t.Error("the captured effect is not shown; a dry run that reports nothing tells the operator nothing")
	}
}

// Spend without its ceiling means nothing, and a ceiling without its spend
// hides the flow living at the edge of one.
func TestSpendIsShownBesideItsCeiling(t *testing.T) {
	runs := []vayuflow.Run{{
		ID: "run-1", FlowID: "flow-1", FlowVersion: 1, Mode: vayuflow.RunLive,
		Status: vayuflow.StatusSucceeded, Owner: "u", OwnerRole: "admin",
		Budget:    vayuflow.Budget{MaxStepsPerRun: 4, MaxWritesPerRun: 20, MaxEgressPerRun: 1},
		Spend:     vayuflow.Spend{Steps: 3, Writes: 3, Egress: 0},
		StartedAt: time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC),
	}}
	page := renderPage(t, nil, nil, vayuflow.Stats{}, runs, true)
	if !strings.Contains(page, "3 / 20") {
		t.Error(`the trail must read "writes: 3 / 20" — a count without its ceiling means nothing`)
	}
}

// The run trail must record the role the run ACTUALLY executed with, so a flow
// that stopped working after a demotion is a visible fact rather than an
// inference.
func TestTheTrailShowsTheResolvedOwnerRole(t *testing.T) {
	runs := []vayuflow.Run{{
		ID: "r", FlowID: "f", FlowVersion: 1, Mode: vayuflow.RunLive,
		Status: vayuflow.StatusRefused, Owner: "user-9", OwnerRole: "author",
		Error:     "owner user-9 no longer holds the editor authority this flow's actions require",
		StartedAt: time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC),
	}}
	page := renderPage(t, nil, nil, vayuflow.Stats{}, runs, true)
	if !strings.Contains(page, "user-9") || !strings.Contains(page, "author") {
		t.Error("the trail must name the owner and the role that was resolved")
	}
	if !strings.Contains(page, "no longer holds") {
		t.Error("the refusal reason must be shown")
	}
}

// A flow the engine will not fire must be NAMED. This is the state the whole
// subsystem is built to make impossible to have without noticing.
func TestAFlowThatCannotRunIsNamedOnThePage(t *testing.T) {
	page := renderPage(t, nil,
		map[string]error{"flow-broken": errors.New("no registered capability for action \"content.publish.now\"")},
		vayuflow.Stats{}, nil, true)
	if !strings.Contains(page, "flow-broken") {
		t.Error("a flow that cannot run was not named on the page")
	}
	if !strings.Contains(page, "cannot run") {
		t.Error("the section must say what it is listing")
	}
}

// The capability registry is shown in full, including the fact that admin
// actions do not exist. A page that listed only what CAN be automated would
// leave "can it change my settings?" unanswered.
func TestThePageStatesWhatCannotBeAutomated(t *testing.T) {
	page := renderPage(t, nil, nil, vayuflow.Stats{}, nil, true)
	for _, want := range []string{"content.draft.create", "content.draft.update"} {
		if !strings.Contains(page, want) {
			t.Errorf("the registry table omits %q", want)
		}
	}
	if !strings.Contains(page, "cannot be automated") {
		t.Error("the page must say that settings, users, keys and payment config are not automatable")
	}
}

// An install where the engine did not start must say so rather than showing an
// empty list that reads as "no automations".
func TestAnUnwiredEngineSaysSo(t *testing.T) {
	page := renderPage(t, nil, nil, vayuflow.Stats{}, nil, false)
	if !strings.Contains(page, "not running in this build") {
		t.Error("an unwired engine must be stated, not left to look like an empty list")
	}
	if strings.Contains(renderPage(t, nil, nil, vayuflow.Stats{}, nil, true), "not running in this build") {
		t.Error("a wired engine must not carry the warning")
	}
}

// House style, per the repository's standing page rules.
func TestThePageFollowsTheHouseStyle(t *testing.T) {
	page := renderPage(t, []vayuflow.Flow{samplePageFlow()}, nil, vayuflow.Stats{}, nil, true)
	for _, want := range []string{
		`<div class="page-header">`, `<p class="page-sub">`,
		`<div class="stat-grid">`, `class="section-head"`, `class="mon-stack"`,
		`role="status"`, `aria-live="polite"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("house style: the page is missing %s", want)
		}
	}
	// assertCSPSafe forbids these; checked here too so a failure names the page.
	if strings.Contains(page, `style="`) {
		t.Error("the page carries an inline style attribute, which the CSP refuses")
	}
	for _, forbidden := range []string{"cdn", "googleapis", "unpkg", "jsdelivr"} {
		if strings.Contains(strings.ToLower(page), forbidden) {
			t.Errorf("the page mentions %q, which assertCSPSafe forbids", forbidden)
		}
	}
}

// Arming must not guess. An unrecognised mode has to be refused, because
// defaulting to dry-run would be the safe-LOOKING choice and would still be the
// endpoint answering a question the operator asked.
func TestArmingRefusesAnUnrecognisedMode(t *testing.T) {
	for _, good := range []struct {
		in   string
		want vayuflow.RunMode
	}{
		{"live", vayuflow.RunLive},
		{"dry", vayuflow.RunDryRun},
		{"dry-run", vayuflow.RunDryRun},
		{"LIVE", vayuflow.RunLive},
	} {
		got, ok := armModeFor(good.in)
		if !ok || got != good.want {
			t.Errorf("armModeFor(%q) = %v,%v; want %v,true", good.in, got, ok, good.want)
		}
	}
	for _, bad := range []string{"", "on", "1", "yes", "enabled", "true", "arm"} {
		if _, ok := armModeFor(bad); ok {
			t.Errorf("armModeFor(%q) was accepted; an unrecognised mode must change nothing", bad)
		}
	}
}

// The nav key must be admin-gated. An author who could reach the flow editor
// could arm work that runs with their own authority on a schedule.
func TestVayuFlowIsAdminOnly(t *testing.T) {
	if got := osPathMinLevel("/os/vayuflow"); got != accessAdmin {
		t.Errorf("/os/vayuflow resolves to %v, want accessAdmin — an author reaching this page could "+
			"arm an automation that runs as themselves", got)
	}
	for _, p := range []string{"/os/api/vayuflow/arm", "/os/api/vayuflow/run"} {
		if got := osPathMinLevel(p); got != accessAdmin {
			t.Errorf("%s resolves to %v, want accessAdmin", p, got)
		}
	}
}

// Placement, decided deliberately and pinned so it does not drift.
//
// VayuFlow is install-scoped work an operator ARMS and INSPECTS, which is what
// the Operations hub's "Controls & diagnostics" band is for. Optimize is
// site-scoped — it fronts "Your websites" — so filing it there would imply an
// automation belongs to one site. And it stays OUT of the sidebar: the sidebar
// is the short list of daily surfaces, and an automation console is not one.
func TestVayuFlowIsUnderOperationsAndNotInTheSidebar(t *testing.T) {
	ops := repoFile(t, "cmd/vayupress/admin_os_operations.go")
	if !strings.Contains(ops, `"/os/vayuflow"`) {
		t.Error("VayuFlow has no card on the Operations hub")
	}
	// It must sit in the FIRST band (Controls & diagnostics), not in Health &
	// governance where posture pages live.
	controls := strings.Index(ops, "Controls &amp; diagnostics")
	health := strings.Index(ops, "Health &amp; governance")
	card := strings.Index(ops, `"/os/vayuflow"`)
	if controls < 0 || health < 0 || card < 0 {
		t.Fatalf("could not locate the bands or the card (controls=%d health=%d card=%d)", controls, health, card)
	}
	if !(card > controls && card < health) {
		t.Error("the VayuFlow card is not in the Controls & diagnostics band")
	}

	ui := repoFile(t, "cmd/vayupress/admin_os_ui.go")
	if strings.Contains(ui, `navItem("/os/vayuflow"`) {
		t.Error("VayuFlow is in the sidebar; it belongs under Operations only")
	}
	// The route must still exist — "not in the sidebar" must not become
	// "unreachable".
	if !strings.Contains(ui, `Get("/os/vayuflow"`) {
		t.Error("the /os/vayuflow route is missing")
	}
}

// The posture report must reach the page, and the honest ceiling must be on it.
// A report computed and not rendered is the same as no report.
func TestThePosturePageCarriesTheHonestCeiling(t *testing.T) {
	page := renderPage(t, []vayuflow.Flow{samplePageFlow()}, nil, vayuflow.Stats{}, nil, true)
	if !strings.Contains(page, "What this report does not cover") {
		t.Fatal("the honest ceiling is not rendered on the page")
	}
	if !strings.Contains(page, "taken over") {
		t.Error("the page does not say the engine is no defence against a compromised account")
	}
	// And it must not be toned as a success.
	i := strings.Index(page, "What this report does not cover")
	window := page[clampLo(i-400) : i+200]
	if strings.Contains(window, "mon-chip--on") {
		t.Error("the honest ceiling is toned as a pass; it is a fact, not reassurance")
	}
}

func clampLo(i int) int {
	if i < 0 {
		return 0
	}
	return i
}

// Only Pass gets the "on" tone. A Context row toned green would let a caveat
// read as reassurance — the exact overstatement this project treats as a defect.
func TestOnlyAPassIsTonedAsSuccess(t *testing.T) {
	if !strings.Contains(flowCheckChip(flowaudit.Pass), "mon-chip--on") {
		t.Error("a pass should be toned on")
	}
	for _, s := range []flowaudit.Status{flowaudit.Warn, flowaudit.Fail, flowaudit.Context} {
		if strings.Contains(flowCheckChip(s), "mon-chip--on") {
			t.Errorf("%s is toned as a success", s)
		}
	}
}
