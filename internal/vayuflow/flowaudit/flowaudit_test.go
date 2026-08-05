// SPDX-License-Identifier: Apache-2.0

package flowaudit

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayuflow"
)

func flow(name string, mode vayuflow.RunMode, steps ...string) vayuflow.Flow {
	f := vayuflow.Flow{
		ID: name, Name: name, Enabled: true, Mode: mode, Owner: "u1", Version: 1,
		Trigger: vayuflow.Trigger{Kind: vayuflow.TriggerManual},
		Budget: vayuflow.Budget{MaxStepsPerRun: 4, MaxRunsPerHour: 2, MaxWritesPerRun: 2,
			MaxEgressPerRun: 2, Timeout: time.Minute},
	}
	for _, a := range steps {
		f.Steps = append(f.Steps, vayuflow.Step{Action: a})
	}
	return f
}

// find returns the ONE check with this title. An assertion that cannot say
// which row it matched is not an assertion — searching the whole report for a
// word passes on any row that happens to use it.
func find(t *testing.T, checks []Check, title string) Check {
	t.Helper()
	var got []Check
	for _, c := range checks {
		if c.Title == title {
			got = append(got, c)
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one %q row, got %d", title, len(got))
	}
	return got[0]
}

// The honest ceiling is the row the ADR insists on, and it must never be a
// Pass: a posture panel that implies it defends against a compromised operator
// account is the same defect class as a report that overstates what is
// enforcing.
func TestTheHonestCeilingIsAlwaysPresentAndNeverAPass(t *testing.T) {
	for _, in := range []Inputs{
		{Wired: true},
		{Wired: true, Flows: []vayuflow.Flow{flow("a", vayuflow.RunLive, "content.draft.create")}},
		{Wired: true, OnionMode: true, ModelConfigured: true, ModelLocal: true},
	} {
		checks := Run(in)
		c := find(t, checks, "What this report does not cover")
		if c.Status == Pass {
			t.Error("the honest ceiling was reported as a pass")
		}
		if !strings.Contains(c.Detail, "taken over") {
			t.Errorf("the ceiling must name account takeover in plain words, got: %q", c.Detail)
		}
		if !strings.Contains(strings.ToLower(c.Detail), "any good") {
			t.Errorf("the ceiling must say it judges authority and not quality, got: %q", c.Detail)
		}
	}
}

// An engine that is not running must say so as a FAIL and stop, rather than
// producing a page of green rows about flows that cannot fire.
func TestAnUnwiredEngineFailsAndReportsNothingElse(t *testing.T) {
	checks := Run(Inputs{Wired: false, Flows: []vayuflow.Flow{flow("a", vayuflow.RunLive, "content.draft.create")}})
	if len(checks) != 1 {
		t.Fatalf("an unwired engine should report exactly one row, got %d", len(checks))
	}
	if checks[0].Status != Fail {
		t.Errorf("an unwired engine is a Fail, got %s", checks[0].Status)
	}
}

// Authority that has outlived its grant is a Fail and NAMES the flow. A count
// alone would send an operator to read the flow list.
func TestStaleOwnerAuthorityIsAFailAndNamesTheFlow(t *testing.T) {
	in := Inputs{
		Wired: true,
		Flows: []vayuflow.Flow{flow("digest", vayuflow.RunLive, "content.draft.create")},
		// content.draft.create needs editor; the owner is now an author.
		OwnerRoles: map[string]string{"u1": vayuflow.RoleAuthor},
	}
	c := find(t, Run(in), "Owner authority")
	if c.Status != Fail {
		t.Fatalf("a demoted owner must be a Fail, got %s", c.Status)
	}
	if !strings.Contains(c.Detail, "digest") {
		t.Errorf("the row must name the flow, got: %q", c.Detail)
	}

	// And an owner who cannot be resolved at all fails the same way — "we could
	// not check" must never read as "fine".
	in.OwnerRoles = map[string]string{}
	if c := find(t, Run(in), "Owner authority"); c.Status != Fail {
		t.Errorf("an unresolvable owner must be a Fail, got %s", c.Status)
	}

	in.OwnerRoles = map[string]string{"u1": vayuflow.RoleAdmin}
	if c := find(t, Run(in), "Owner authority"); c.Status != Pass {
		t.Errorf("a still-authorised owner should pass, got %s (%s)", c.Status, c.Detail)
	}
}

// A ceiling reached is never reported as a success.
func TestAReachedCeilingIsAWarnNotAPass(t *testing.T) {
	c := find(t, Run(Inputs{Wired: true, Stats: vayuflow.Stats{Runs: 10, BudgetCapped: 3}}), "Budgets")
	if c.Status != Warn {
		t.Fatalf("a reached ceiling must warn, got %s", c.Status)
	}
	if !strings.Contains(c.Detail, "3 of 10") {
		t.Errorf("the row must carry the numbers, got: %q", c.Detail)
	}
	if c := find(t, Run(Inputs{Wired: true, Stats: vayuflow.Stats{Runs: 10}}), "Budgets"); c.Status != Pass {
		t.Errorf("no reached ceiling should pass, got %s", c.Status)
	}
}

// Egress under a Tor Space must be reported as PRESENT AND CLOSED, not as a
// clean pass. The flows exist and would reach out if the mode changed.
func TestEgressUnderTorIsReportedAsPresentAndInert(t *testing.T) {
	in := Inputs{
		Wired:     true,
		OnionMode: true,
		Flows:     []vayuflow.Flow{flow("sync", vayuflow.RunLive, "egress.fetch")},
	}
	c := find(t, Run(in), "Outbound reach")
	if c.Status == Pass {
		t.Error("an egress-capable flow under Tor was reported as a clean pass; it exists and is closed")
	}
	if !strings.Contains(c.Detail, "INERT") {
		t.Errorf("the row must say the steps are inert, got: %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "sync") {
		t.Errorf("the row must name the flow, got: %q", c.Detail)
	}

	// Same flow on a clearnet install: a warning, because it really does reach out.
	in.OnionMode = false
	if c := find(t, Run(in), "Outbound reach"); c.Status != Warn {
		t.Errorf("an egress flow on clearnet should warn, got %s", c.Status)
	}

	// No egress flows at all: a genuine pass.
	in.Flows = []vayuflow.Flow{flow("draft", vayuflow.RunLive, "content.draft.create")}
	if c := find(t, Run(in), "Outbound reach"); c.Status != Pass {
		t.Errorf("no outbound flows should pass, got %s", c.Status)
	}
}

// "AI: enabled" is the overstatement the ADR names. The row must distinguish a
// local provider from a remote one, because that is the difference between a
// generation that stays on this host and one that leaves it.
func TestTheModelRowDistinguishesLocalFromRemote(t *testing.T) {
	base := Inputs{Wired: true, ModelConfigured: true,
		Flows: []vayuflow.Flow{flow("write", vayuflow.RunLive, "model.draft.generate")}}

	local := base
	local.ModelLocal = true
	c := find(t, Run(local), "Model steps")
	if c.Status != Pass || !strings.Contains(c.Detail, "LOCAL") {
		t.Errorf("a local provider should pass and say so, got %s / %q", c.Status, c.Detail)
	}

	remote := base
	remote.ModelLocal = false
	c = find(t, Run(remote), "Model steps")
	if c.Status != Warn || !strings.Contains(c.Detail, "REMOTE") {
		t.Errorf("a remote provider should warn and say so, got %s / %q", c.Status, c.Detail)
	}

	// Remote provider in a Tor Space: those steps are refused, so the flows
	// fail. That is a Fail, and the row must say they fail RATHER THAN LEAK.
	torRemote := remote
	torRemote.OnionMode = true
	c = find(t, Run(torRemote), "Model steps")
	if c.Status != Fail {
		t.Fatalf("a remote provider in a Tor Space must fail, got %s", c.Status)
	}
	if !strings.Contains(c.Detail, "rather than leak") {
		t.Errorf("the row must say the flows fail rather than leak, got: %q", c.Detail)
	}

	// A model flow with no provider configured at all fails outright.
	none := base
	none.ModelConfigured = false
	if c := find(t, Run(none), "Model steps"); c.Status != Fail {
		t.Errorf("a model flow with no provider must fail, got %s", c.Status)
	}
}

// The row that exists because mail widened what a model value can touch.
func TestModelOutputReachingAnIrreversibleActionIsSurfaced(t *testing.T) {
	in := Inputs{Wired: true, ModelConfigured: true, ModelLocal: true,
		Flows: []vayuflow.Flow{flow("digest", vayuflow.RunLive,
			"model.draft.generate", "mail.send")}}
	c := find(t, Run(in), "Model output reaching an irreversible action")
	if c.Status != Warn {
		t.Errorf("expected a warn, got %s", c.Status)
	}
	if !strings.Contains(c.Detail, "digest") {
		t.Errorf("the row must name the flow, got: %q", c.Detail)
	}
	// It must not claim the draft ceiling covers it — that is the overstatement
	// this row exists to avoid.
	if !strings.Contains(c.Detail, "no draft form") {
		t.Errorf("the row must say the draft ceiling does not apply, got: %q", c.Detail)
	}

	// A dry-run flow is not armed, so it does not appear.
	in.Flows[0].Mode = vayuflow.RunDryRun
	for _, ch := range Run(in) {
		if ch.Title == "Model output reaching an irreversible action" {
			t.Error("a dry-run flow was reported as reaching an irreversible action")
		}
	}
}

// A flow that will not fire must be named, not counted.
func TestABrokenDefinitionIsNamed(t *testing.T) {
	c := find(t, Run(Inputs{Wired: true,
		Rejected: map[string]error{"flow-7": errors.New("no registered capability")}}), "Definitions")
	if c.Status != Fail {
		t.Fatalf("a broken definition must fail, got %s", c.Status)
	}
	if !strings.Contains(c.Detail, "flow-7") {
		t.Errorf("the row must name the flow, got: %q", c.Detail)
	}
}

// A backlog that is not shrinking means the drainer has stopped.
func TestAGrowingEventBacklogWarns(t *testing.T) {
	if c := find(t, Run(Inputs{Wired: true, PendingInbox: 500}), "Event backlog"); c.Status != Warn {
		t.Errorf("a large backlog should warn, got %s", c.Status)
	}
	if c := find(t, Run(Inputs{Wired: true}), "Event backlog"); c.Status != Pass {
		t.Errorf("an empty backlog should pass, got %s", c.Status)
	}
}

// Every check must carry prose. A status with no detail is a colour, not a
// finding — and a panel of colours is what an operator stops reading.
func TestEveryCheckExplainsItself(t *testing.T) {
	in := Inputs{Wired: true, ModelConfigured: true, OnionMode: true,
		Flows: []vayuflow.Flow{
			flow("a", vayuflow.RunLive, "model.draft.generate", "mail.send"),
			flow("b", vayuflow.RunLive, "egress.fetch"),
		},
		OwnerRoles: map[string]string{"u1": vayuflow.RoleAdmin},
		Stats:      vayuflow.Stats{Runs: 4, BudgetCapped: 1},
	}
	checks := Run(in)
	if len(checks) < 6 {
		t.Fatalf("only %d checks ran; the report is thinner than it looks", len(checks))
	}
	for _, c := range checks {
		if strings.TrimSpace(c.Title) == "" {
			t.Error("a check has no title")
		}
		if len(strings.TrimSpace(c.Detail)) < 40 {
			t.Errorf("%q has no real explanation: %q", c.Title, c.Detail)
		}
		if c.Status == statusUnset {
			t.Errorf("%q has no verdict", c.Title)
		}
	}
}

func TestSummaryCounts(t *testing.T) {
	checks := []Check{{Status: Pass}, {Status: Pass}, {Status: Warn}, {Status: Fail}, {Status: Context}}
	p, w, f, c := Summary(checks)
	if p != 2 || w != 1 || f != 1 || c != 1 {
		t.Errorf("Summary = %d/%d/%d/%d", p, w, f, c)
	}
}
