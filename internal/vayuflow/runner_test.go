// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// The test actions register against real capabilities, so the registry rule —
// no implementation without a declared blast radius — holds in the tests too.
// Registering a fake against a fake capability would let these tests pass on an
// engine that had lost the rule.
var testActionCalls struct {
	sync.Mutex
	n int
}

func init() {
	RegisterAction("content.draft.create", func(_ context.Context, p map[string]string, e *Effects) (string, error) {
		testActionCalls.Lock()
		testActionCalls.n++
		testActionCalls.Unlock()
		if err := e.Write(WriteDraft, "create draft "+p["title"]); err != nil {
			if errors.Is(err, ErrDryRun) {
				return "captured", err
			}
			return "", err
		}
		return "created", nil
	})
	RegisterAction("content.draft.update", func(_ context.Context, p map[string]string, e *Effects) (string, error) {
		if err := e.Write(WriteDraft, "update draft "+p["slug"]); err != nil {
			return "", err
		}
		return "updated", nil
	})
}

func newTestRig(t *testing.T, role string) (*Store, *RunStore, *Runner) {
	t.Helper()
	db := newTestDB(t)
	fs, rs := NewStore(db), NewRunStore(db)
	rn := NewRunner(fs, rs, func(context.Context, string) (string, error) { return role, nil })
	return fs, rs, rn
}

func armedFlow(t *testing.T, fs *Store, mode RunMode) Flow {
	t.Helper()
	f := goodFlow()
	f.Enabled, f.Mode = true, mode
	if err := fs.Save(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// THE P2 GATE. The failure operators actually fear is the newsletter that went
// out twice; redelivery must not be able to produce a second run.
func TestRedeliveringTheSameEventProducesExactlyOneRun(t *testing.T) {
	fs, rs, rn := newTestRig(t, RoleAdmin)
	ctx := context.Background()
	f := armedFlow(t, fs, RunLive)

	first, err := rn.Execute(ctx, f, "event", "inbox-row-42", Subject{})
	if err != nil {
		t.Fatalf("first delivery must run: %v", err)
	}
	if first.Status != StatusSucceeded {
		t.Fatalf("first run should have succeeded, got %s (%s)", first.Status, first.Error)
	}

	// Same event, delivered again — a duplicate webhook, a retried drain, a
	// process that crashed after acting but before acknowledging.
	second, err := rn.Execute(ctx, f, "event", "inbox-row-42", Subject{})
	if !errors.Is(err, ErrDuplicateRun) {
		t.Fatalf("redelivery must collide on the idempotency key, got run=%v err=%v", second, err)
	}

	runs, err := rs.Recent(ctx, f.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("redelivery produced %d runs; exactly one is the whole property", len(runs))
	}
}

// The mirror of the gate: pressing Run twice IS two runs, and an idempotency
// scheme too coarse to tell them apart would swallow legitimate work.
func TestPressingRunTwiceProducesTwoRuns(t *testing.T) {
	fs, rs, rn := newTestRig(t, RoleAdmin)
	ctx := context.Background()
	f := armedFlow(t, fs, RunLive)

	for i, id := range []string{"manual-1", "manual-2"} {
		if _, err := rn.Execute(ctx, f, "manual", id, Subject{}); err != nil {
			t.Fatalf("manual run %d: %v", i+1, err)
		}
	}
	runs, _ := rs.Recent(ctx, f.ID, 10)
	if len(runs) != 2 {
		t.Fatalf("two distinct manual runs produced %d rows", len(runs))
	}
}

// An edit bumps the version, and the key carries the version — so the same
// event delivered to an EDITED flow is new work rather than a duplicate. This
// is the boundary the key derivation has to get right in both directions.
func TestEditingAFlowMakesTheSameEventNewWork(t *testing.T) {
	fs, rs, rn := newTestRig(t, RoleAdmin)
	ctx := context.Background()
	f := armedFlow(t, fs, RunLive)

	if _, err := rn.Execute(ctx, f, "event", "inbox-row-7", Subject{}); err != nil {
		t.Fatal(err)
	}
	f.Name = "Edited"
	if err := fs.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if _, err := rn.Execute(ctx, f, "event", "inbox-row-7", Subject{}); err != nil {
		t.Fatalf("after an edit the same event is different work: %v", err)
	}
	runs, _ := rs.Recent(ctx, f.ID, 10)
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs across the edit, got %d", len(runs))
	}
}

// A run whose process died resumes as interrupted — not failed, not succeeded —
// and is never retried, because a step that already sent mail must not be
// replayed by a ticker.
func TestACrashLeavesRunsInterruptedAndNotRetried(t *testing.T) {
	db := newTestDB(t)
	rs := NewRunStore(db)
	fs := NewStore(db)
	ctx := context.Background()
	f := armedFlow(t, fs, RunLive)

	run, err := rs.Begin(ctx, f, "event", "inbox-row-9", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusRunning {
		t.Fatalf("a begun run should be running, got %s", run.Status)
	}

	// The process dies here. Restart:
	n, err := rs.RecoverInterrupted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("recovery converted %d runs, want 1", n)
	}
	runs, _ := rs.Recent(ctx, f.ID, 10)
	if len(runs) != 1 || runs[0].Status != StatusInterrupted {
		t.Fatalf("run should be interrupted, got %+v", runs)
	}
	if runs[0].Error == "" {
		t.Error("an interrupted run must say why, or an operator cannot tell it from a hang")
	}
	// And the key is still claimed, so nothing replays it behind the operator's
	// back — that is what "never retried automatically" has to mean in practice.
	if _, err := rs.Begin(ctx, f, "event", "inbox-row-9", RoleAdmin); !errors.Is(err, ErrDuplicateRun) {
		t.Fatal("an interrupted run's idempotency key must stay claimed, or a ticker can replay it")
	}
}

// Authority is re-checked against the LIVE account on every run. A flow armed
// by an admin who is later demoted has to stop working; without this a flow is
// a permanent capability grant that outlives the grant.
func TestAFlowStopsWhenItsOwnerIsDemoted(t *testing.T) {
	db := newTestDB(t)
	fs, rs := NewStore(db), NewRunStore(db)
	role := RoleAdmin
	rn := NewRunner(fs, rs, func(context.Context, string) (string, error) { return role, nil })
	ctx := context.Background()
	f := armedFlow(t, fs, RunLive)

	if run, err := rn.Execute(ctx, f, "manual", "m1", Subject{}); err != nil || run.Status != StatusSucceeded {
		t.Fatalf("an admin-owned flow should run: %v %+v", err, run)
	}

	role = RoleAuthor // demoted; the flow's actions need editor

	run, err := rn.Execute(ctx, f, "manual", "m2", Subject{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusRefused {
		t.Fatalf("a demoted owner's flow must refuse, got %s", run.Status)
	}
	if !strings.Contains(run.Error, "authority") {
		t.Errorf("the refusal must say it was about authority, got %q", run.Error)
	}
	// The trail records the role that was actually resolved, so the refusal is
	// a fact afterwards rather than an inference.
	if run.OwnerRole != RoleAuthor {
		t.Errorf("the run should record the resolved role %q, got %q", RoleAuthor, run.OwnerRole)
	}
}

// "We could not check whether this account still has authority" must never read
// as "yes".
func TestAnUnresolvableOwnerFailsClosed(t *testing.T) {
	db := newTestDB(t)
	fs, rs := NewStore(db), NewRunStore(db)
	rn := NewRunner(fs, rs, func(context.Context, string) (string, error) {
		return "", errors.New("account store unavailable")
	})
	ctx := context.Background()
	f := armedFlow(t, fs, RunLive)

	run, err := rn.Execute(ctx, f, "manual", "m1", Subject{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusRefused {
		t.Fatalf("an unresolvable owner must refuse, got %s", run.Status)
	}
}

// Attack #1 from the ADR: a trigger storm must not grow the trail without
// bound. The ceiling is checked before any row is written, so the table is
// bounded at MaxRunsPerHour per flow per hour by construction.
func TestATriggerStormIsBoundedAndWritesNoRunawayRows(t *testing.T) {
	fs, rs, rn := newTestRig(t, RoleAdmin)
	ctx := context.Background()
	f := armedFlow(t, fs, RunLive) // MaxRunsPerHour = 2

	var refused int
	for i := 0; i < 500; i++ {
		_, err := rn.Execute(ctx, f, "event", fmt.Sprintf("storm-%d", i), Subject{})
		if errors.Is(err, ErrRateCeiling) {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("500 triggers produced no rate refusals; the ceiling is not holding")
	}
	runs, err := rs.Recent(ctx, f.ID, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) > f.Budget.MaxRunsPerHour {
		t.Fatalf("a storm of 500 wrote %d run rows against a ceiling of %d; the trail grows without bound",
			len(runs), f.Budget.MaxRunsPerHour)
	}
}

// Attack #7, the dry-run lie: a dry run must execute the WHOLE flow and capture
// what it would have done. One that skipped the work would tell the operator
// nothing about the live run.
func TestADryRunExecutesEverythingAndCapturesTheDiff(t *testing.T) {
	fs, _, rn := newTestRig(t, RoleAdmin)
	ctx := context.Background()
	f := armedFlow(t, fs, RunDryRun)

	testActionCalls.Lock()
	before := testActionCalls.n
	testActionCalls.Unlock()

	run, err := rn.Execute(ctx, f, "manual", "dry-1", Subject{})
	if err != nil {
		t.Fatal(err)
	}
	testActionCalls.Lock()
	after := testActionCalls.n
	testActionCalls.Unlock()

	if after != before+1 {
		t.Error("a dry run must actually call the action; skipping it is the dry-run lie")
	}
	if run.Status != StatusSucceeded {
		t.Fatalf("a dry run that reached the end is a success, got %s (%s)", run.Status, run.Error)
	}
	if len(run.Steps) != 1 || run.Steps[0].Refused == "" {
		t.Fatalf("a dry run must capture what it would have done, got %+v", run.Steps)
	}
	if !strings.Contains(run.Steps[0].Refused, "would write") {
		t.Errorf("the captured effect should describe the change, got %q", run.Steps[0].Refused)
	}
	// And it must SPEND the budget, or it under-reports what the live run costs.
	if run.Spend.Writes != 1 {
		t.Errorf("a dry run must charge its budget, got writes=%d", run.Spend.Writes)
	}
}

// A live run performs the effect and records the spend against its ceiling.
func TestALiveRunPerformsTheEffectAndRecordsSpend(t *testing.T) {
	fs, rs, rn := newTestRig(t, RoleAdmin)
	ctx := context.Background()
	f := armedFlow(t, fs, RunLive)

	run, err := rn.Execute(ctx, f, "manual", "live-1", Subject{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Steps[0].Refused != "" {
		t.Errorf("a live run must not capture-and-refuse, got %q", run.Steps[0].Refused)
	}
	if run.Steps[0].Output != "created" {
		t.Errorf("the step's output should be recorded, got %q", run.Steps[0].Output)
	}
	if run.Spend.Writes != 1 || run.Spend.Steps != 1 {
		t.Errorf("spend not recorded: %+v", run.Spend)
	}
	// Reload from the trail — an in-memory value proves nothing about what an
	// operator will read tomorrow.
	stored, err := rs.Recent(ctx, f.ID, 1)
	if err != nil || len(stored) != 1 {
		t.Fatalf("recent: %v %d", err, len(stored))
	}
	if stored[0].Spend.Writes != 1 || stored[0].Budget.MaxWritesPerRun != f.Budget.MaxWritesPerRun {
		t.Errorf("the trail must carry spend AND its ceiling: %+v / %+v", stored[0].Spend, stored[0].Budget)
	}
}

// A condition that does not hold is a refusal, recorded as such — not a
// failure. A refusal that looked like a failure would train an operator to
// ignore failures.
func TestAConditionThatDoesNotHoldRefusesRatherThanFails(t *testing.T) {
	fs, _, rn := newTestRig(t, RoleAdmin)
	ctx := context.Background()
	f := goodFlow()
	f.Enabled, f.Mode = true, RunLive
	f.Condition = Condition{Kind: CondStatusIs, Value: "published"}
	if err := fs.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	run, err := rn.Execute(ctx, f, "event", "cond-1", Subject{Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusRefused {
		t.Fatalf("a condition that did not hold should refuse, got %s", run.Status)
	}
	if run.Spend.Steps != 0 {
		t.Errorf("a refused run must not have spent anything, got %+v", run.Spend)
	}
}

// The step ceiling bounds expansion inside the runner, not only in the planner.
func TestTheRunnerChargesTheStepCeiling(t *testing.T) {
	fs, _, rn := newTestRig(t, RoleAdmin)
	ctx := context.Background()
	f := goodFlow()
	f.Enabled, f.Mode = true, RunLive
	f.Steps = []Step{
		{Action: "content.draft.create"},
		{Action: "content.draft.update"},
	}
	f.Budget.MaxStepsPerRun = 2
	f.Budget.MaxWritesPerRun = 2
	if err := fs.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	run, err := rn.Execute(ctx, f, "manual", "steps-1", Subject{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Spend.Steps != 2 {
		t.Errorf("both steps should have been charged, got %d", run.Spend.Steps)
	}
	if run.Status != StatusSucceeded {
		t.Fatalf("two steps within a ceiling of two should succeed, got %s (%s)", run.Status, run.Error)
	}
}

// Attack #2: an action must not be able to write beyond its capability's
// ceiling, whatever it passes.
func TestAnActionCannotWriteAboveItsCapabilityCeiling(t *testing.T) {
	capab, err := CapabilityFor("content.draft.create")
	if err != nil {
		t.Fatal(err)
	}
	var spend Spend
	e := &Effects{mode: RunLive, cap: capab, budget: testBudget(), spend: &spend}
	// The capability is writeDraft. Attempting live must be refused by the
	// effect path itself, not by the action being well behaved.
	if err := e.Write(WriteLive, "publish"); err == nil {
		t.Fatal("a draft-capped action published")
	}
	if spend.Writes != 0 {
		t.Errorf("a refused write must not be charged, got %d", spend.Writes)
	}
}

// Every implemented action must have a declared capability. This is the
// direction that matters for safety: an implementation without a contract could
// run with no declared blast radius at all.
func TestEveryImplementedActionHasACapability(t *testing.T) {
	impls := ImplementedActions()
	if len(impls) == 0 {
		t.Fatal("no actions are implemented; this test is proving nothing")
	}
	for _, name := range impls {
		if _, err := CapabilityFor(name); err != nil {
			t.Errorf("action %q is implemented but has no capability: %v", name, err)
		}
	}
}

// An egress action must refuse in a Tor Space regardless of what the call site
// does, so the guarantee does not depend on a caller remembering.
func TestEgressRefusesInATorSpace(t *testing.T) {
	orig := clearnetBlocked
	clearnetBlocked = func() bool { return true }
	t.Cleanup(func() { clearnetBlocked = orig })

	capab := Capability{
		Action: "test.fetch", Kind: KindEgress, Writes: WriteNone,
		Onion: OnionInert, Undo: ReversibleByOperator, MinRole: RoleAdmin,
		Rationale: "fixture",
	}
	var spend Spend
	e := &Effects{mode: RunLive, cap: capab, budget: testBudget(), spend: &spend}
	err := e.Fetch("https://example.invalid/x")
	if err == nil {
		t.Fatal("an egress action fetched in a Tor Space")
	}
	if errors.Is(err, ErrDryRun) {
		t.Fatal("the Tor refusal must not be reported as a dry-run capture")
	}
	if spend.Egress != 0 {
		t.Errorf("a refused fetch must not be charged, got %d", spend.Egress)
	}
}

// Registering an implementation for an action with no capability is a startup
// panic, not a silent skip: a flow that does nothing for a reason nobody can
// see is worse than a crash at boot.
func TestRegisteringAnActionWithNoCapabilityPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering an action with no capability was permitted")
		}
	}()
	RegisterAction("content.publish.now", func(context.Context, map[string]string, *Effects) (string, error) {
		return "", nil
	})
}

// A run cannot be finished while it still claims to be running; that would put
// a permanently-running row in the trail which recovery would later convert to
// interrupted, misreporting a completed run as a crash.
func TestFinishRefusesARunStillMarkedRunning(t *testing.T) {
	db := newTestDB(t)
	fs, rs := NewStore(db), NewRunStore(db)
	ctx := context.Background()
	f := armedFlow(t, fs, RunLive)
	run, err := rs.Begin(ctx, f, "manual", "x1", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.Finish(ctx, run); err == nil {
		t.Fatal("Finish accepted a run still marked running")
	}
}
