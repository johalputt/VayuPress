// SPDX-License-Identifier: Apache-2.0

package vayuflow

// authoring_test.go — the engine half of "an operator can actually use this".
//
// The audit that produced this file found that Store.Save and Store.Delete had
// no non-test caller anywhere in the binary: renaming both left it linking. The
// engine was correct and unreachable. So these tests cover the pieces the new
// authoring surface leans on, and the two runtime gaps found alongside it — a
// disabled flow that ran anyway, and a run trail nothing pruned.

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// ── The disabled flow that ran anyway ───────────────────────────────────────
//
// The ticker and the drainer reach flows through LoadableFlows, which filters
// on Enabled. The manual "Run once now" button loads with Get, which does not.
// So an operator could switch a flow off, watch the panel render its disabled
// chip, press Run, and have it execute — two paths disagreeing about what the
// word means, with the panel telling the reassuring one.
func TestADisabledFlowDoesNotRunOnAnyPath(t *testing.T) {
	db := newTestDB(t)
	fs, rs := NewStore(db), NewRunStore(db)
	rn := NewRunner(fs, rs, func(context.Context, string) (string, error) { return RoleAdmin, nil })
	wireContent(t)

	f := goodFlow()
	f.Enabled, f.Mode = false, RunLive
	if err := fs.Save(context.Background(), &f); err != nil {
		t.Fatal(err)
	}

	_, err := rn.Execute(context.Background(), f, "manual", "off-1", Subject{})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("a disabled flow ran on the manual path; got err=%v", err)
	}

	// And no run row was written. A refusal that fills the trail with rows
	// saying "did not run" buries the runs that did.
	runs, err := rs.Recent(context.Background(), f.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("a disabled flow wrote %d run row(s)", len(runs))
	}

	// Switched on, the same flow runs — or this test would pass against an
	// engine that refuses everything.
	if _, err := fs.SetEnabled(context.Background(), f.ID, true); err != nil {
		t.Fatal(err)
	}
	f.Enabled = true
	if _, err := rn.Execute(context.Background(), f, "manual", "on-1", Subject{}); err != nil {
		t.Fatalf("an enabled flow did not run: %v", err)
	}
}

// SetEnabled returns the PRIOR value, which is what lets the audit entry tell a
// first switch-on from a re-run of the same button.
func TestSetEnabledReportsWhatItChangedFrom(t *testing.T) {
	s := newTestStore(t)
	f := goodFlow()
	f.Enabled = false
	if err := s.Save(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	prior, err := s.SetEnabled(context.Background(), f.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if prior {
		t.Error("switching on a disabled flow reported it was already on")
	}
	again, err := s.SetEnabled(context.Background(), f.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !again {
		t.Error("switching on an already-on flow did not report the prior state, so the audit " +
			"trail cannot tell a first arming from a repeat")
	}
	got, err := s.Get(context.Background(), f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled {
		t.Error("the change was not persisted")
	}
}

// Enabling must not touch the mode, and arming must not touch enabled. They are
// two decisions and the panel offers two buttons; a store that coupled them
// would make one button quietly do the other's job.
func TestEnablingAndArmingAreIndependent(t *testing.T) {
	s := newTestStore(t)
	f := goodFlow()
	f.Enabled, f.Mode = false, RunDryRun
	if err := s.Save(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEnabled(context.Background(), f.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(context.Background(), f.ID)
	if got.Mode != RunDryRun {
		t.Errorf("switching a flow on also armed it: mode is %s", got.Mode)
	}
	if _, err := s.SetMode(context.Background(), f.ID, RunLive); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(context.Background(), f.ID)
	if !got.Enabled {
		t.Error("arming a flow switched it off")
	}
}

// ── The run trail nothing pruned ────────────────────────────────────────────
//
// §7 promises the trail is bounded. The runs-per-hour ceiling bounds the RATE,
// which is what stops a trigger storm, and it was mistaken for a bound on the
// total. Ten runs an hour is eighty-seven thousand rows a year, per flow, kept
// forever.
func TestTheRunTrailForgetsFinishedRunsPastItsWindow(t *testing.T) {
	db := newTestDB(t)
	rs := NewRunStore(db)
	ctx := context.Background()

	old := insertRunAt(t, db, "flow-a", "old-1", StatusSucceeded, -100*24*time.Hour)
	recent := insertRunAt(t, db, "flow-a", "new-1", StatusSucceeded, -1*time.Hour)

	n, err := rs.Prune(ctx, runRetention)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("pruned %d row(s), expected exactly the one past the window", n)
	}
	if runExists(t, db, old) {
		t.Error("a run finished 100 days ago is still in the trail; nothing bounds this table")
	}
	if !runExists(t, db, recent) {
		t.Error("a run from an hour ago was pruned; the window is not being applied")
	}
}

// THE one that matters. A row still marked running is either a live run or one
// interrupted by a crash that RecoverInterrupted has not reconciled yet.
// Deleting either loses exactly the evidence the trail exists for — and an
// interrupted row is old BY DEFINITION, so a prune keyed on age alone eats them
// first.
func TestPruningNeverDeletesARunThatIsStillMarkedRunning(t *testing.T) {
	db := newTestDB(t)
	rs := NewRunStore(db)

	stuck := insertRunAt(t, db, "flow-a", "stuck-1", StatusRunning, -200*24*time.Hour)
	if _, err := rs.Prune(context.Background(), runRetention); err != nil {
		t.Fatal(err)
	}
	if !runExists(t, db, stuck) {
		t.Error("a run left in the running state by a crash was deleted by the retention sweep — " +
			"the interrupted runs are the oldest rows in the table and the ones worth keeping")
	}
	// And once recovery has reconciled it, it becomes prunable like anything
	// else. Otherwise a crashed run is immortal.
	if _, err := rs.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Prune(context.Background(), runRetention); err != nil {
		t.Fatal(err)
	}
	if runExists(t, db, stuck) {
		t.Error("a reconciled interrupted run is never pruned, so it stays forever")
	}
}

// ── The mapping helpers the form depends on ─────────────────────────────────

func TestTriggerKindForRefusesWhatItDoesNotKnow(t *testing.T) {
	for _, ok := range []string{"schedule", "event", "manual", "  MANUAL  "} {
		if _, valid := TriggerKindFor(ok); !valid {
			t.Errorf("%q is a real trigger and was refused", ok)
		}
	}
	for _, bad := range []string{"", "webhook", "cron", "unset", "0"} {
		k, valid := TriggerKindFor(bad)
		if valid {
			t.Errorf("%q was accepted as a trigger and mapped to %s", bad, k)
		}
		// It must not quietly hand back a usable kind alongside ok=false.
		if k != triggerUnset {
			t.Errorf("%q was refused but still returned %s", bad, k)
		}
	}
}

func TestConditionForBuildsOnlyWhatTheEngineHas(t *testing.T) {
	c, err := ConditionFor("always", "")
	if err != nil || c.Kind != CondAlways {
		t.Fatalf("always: %v %v", c, err)
	}
	// Empty kind is "always" — a form that never touched the selector means no
	// condition, which IS an answer here.
	if c, err := ConditionFor("", ""); err != nil || c.Kind != CondAlways {
		t.Errorf("an empty kind should read as always, got %v %v", c, err)
	}
	if _, err := ConditionFor("always", "something"); err == nil {
		t.Error("an always condition accepted a value it will never compare")
	}
	c, err = ConditionFor("tag-equals", "release")
	if err != nil || c.Kind != CondTagEquals || c.Value != "release" {
		t.Fatalf("tag-equals: %v %v", c, err)
	}
	if _, err := ConditionFor("tag-equals", ""); err == nil {
		t.Error("a comparing condition was accepted with nothing to compare against, which would " +
			"match everything with an empty string and read as deliberate")
	}
	// The composite kinds take children a flat form cannot express. Accepting
	// them here would store a condition with no Sub — CondAny with no children
	// NEVER holds, so the flow would silently never fire.
	for _, composite := range []string{"all", "any", "not"} {
		if _, err := ConditionFor(composite, "x"); err == nil {
			t.Errorf("%q was built from a flat form; it takes children and would be stored empty",
				composite)
		}
	}
	if _, err := ConditionFor("no-such-thing", "x"); err == nil {
		t.Error("an unknown condition kind was accepted")
	}
}

// The form's option list is generated from these, so a name that does not round
// trip is an option the store will refuse the moment it is chosen.
func TestEveryOfferedConditionNameCanActuallyBeBuilt(t *testing.T) {
	names := LeafConditionNames()
	if len(names) < 2 {
		t.Fatal("no condition names are offered; this test proves nothing")
	}
	for _, n := range names {
		value := ""
		if n != CondAlways.String() {
			value = "x"
		}
		c, err := ConditionFor(n, value)
		if err != nil {
			t.Errorf("the form offers %q and the engine refuses it: %v", n, err)
			continue
		}
		if c.Kind.String() != n {
			t.Errorf("%q built a %q", n, c.Kind)
		}
		if err := c.Complete(); err != nil {
			t.Errorf("%q built a condition the store will reject: %v", n, err)
		}
	}
}

func TestDescribeTriggerSaysWhichOneAndItsDetail(t *testing.T) {
	for _, tc := range []struct {
		trig Trigger
		want string
	}{
		{Trigger{Kind: TriggerSchedule, Cron: "0 9 * * 1"}, "0 9 * * 1"},
		{Trigger{Kind: TriggerEvent, Event: EventArticleCreated}, EventArticleCreated},
		{Trigger{Kind: TriggerManual}, "manual"},
	} {
		if got := DescribeTrigger(tc.trig); !strings.Contains(got, tc.want) {
			t.Errorf("%v described as %q, missing %q", tc.trig.Kind, got, tc.want)
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func insertRunAt(t *testing.T, db *sql.DB, flowID, key string, st RunStatus, ago time.Duration) string {
	t.Helper()
	id := newID()
	ts := time.Now().UTC().Add(ago).Format(tsLayout)
	_, err := db.Exec(
		`INSERT INTO vayuflow_runs(id,flow_id,flow_version,idempotency_key,trigger_cause,mode,status,
			owner,owner_role,budget_max_steps,budget_max_writes,budget_max_egress,started_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, flowID, 1, key, "test", RunDryRun.String(), string(st),
		"user-1", RoleAdmin, 4, 1, 1, ts)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func runExists(t *testing.T, db *sql.DB, id string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vayuflow_runs WHERE id=?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}
