// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"testing"
	"time"
)

func newTestTicker(t *testing.T, at time.Time) (*Store, *RunStore, *Ticker) {
	t.Helper()
	db := newTestDB(t)
	fs, rs := NewStore(db), NewRunStore(db)
	rn := NewRunner(fs, rs, func(context.Context, string) (string, error) { return RoleAdmin, nil })
	tk := NewTicker(fs, rn, time.UTC)
	tk.now = func() time.Time { return at }
	return fs, rs, tk
}

func scheduleFlow(t *testing.T, fs *Store, cron string, mode RunMode) Flow {
	t.Helper()
	f := goodFlow()
	f.Enabled, f.Mode = true, mode
	f.Trigger = Trigger{Kind: TriggerSchedule, Cron: cron}
	if err := fs.Save(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestATickFiresAMatchingSchedule(t *testing.T) {
	when := at(t, "2026-08-04 09:00")
	fs, rs, tk := newTestTicker(t, when)
	wireContent(t)
	ctx := context.Background()
	f := scheduleFlow(t, fs, "0 9 * * *", RunLive)

	res, err := tk.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Fired != 1 {
		t.Fatalf("expected 1 firing, got %+v", res)
	}
	runs, _ := rs.Recent(ctx, f.ID, 10)
	if len(runs) != 1 || runs[0].Status != StatusSucceeded {
		t.Fatalf("expected one successful run, got %+v", runs)
	}
	if runs[0].Cause == "" {
		t.Error("a scheduled run must record what caused it")
	}
}

func TestATickDoesNotFireANonMatchingSchedule(t *testing.T) {
	fs, _, tk := newTestTicker(t, at(t, "2026-08-04 09:01"))
	wireContent(t)
	scheduleFlow(t, fs, "0 9 * * *", RunLive)

	res, err := tk.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Fired != 0 {
		t.Fatalf("a schedule fired on the wrong minute: %+v", res)
	}
}

// The property that makes the ticker safe without any locking: firing twice in
// the same minute — an overlapping tick, a retry, two processes — produces one
// run, by the SAME mechanism that makes event redelivery safe.
func TestTwoTicksInOneMinuteFireOnce(t *testing.T) {
	when := at(t, "2026-08-04 09:00")
	fs, rs, tk := newTestTicker(t, when)
	wireContent(t)
	ctx := context.Background()
	f := scheduleFlow(t, fs, "0 9 * * *", RunLive)

	first, err := tk.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tk.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fired != 1 || second.Fired != 0 || second.Duplicate != 1 {
		t.Fatalf("second tick in the same minute should be a duplicate: first=%+v second=%+v", first, second)
	}
	runs, _ := rs.Recent(ctx, f.ID, 10)
	if len(runs) != 1 {
		t.Fatalf("two ticks in one minute produced %d runs", len(runs))
	}
}

// A disabled flow is inert. This is the operator's off switch and it must not
// depend on the cron not matching.
func TestADisabledScheduleDoesNotFire(t *testing.T) {
	when := at(t, "2026-08-04 09:00")
	fs, _, tk := newTestTicker(t, when)
	wireContent(t)
	f := goodFlow()
	f.Enabled, f.Mode = false, RunLive
	f.Trigger = Trigger{Kind: TriggerSchedule, Cron: "0 9 * * *"}
	if err := fs.Save(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	res, err := tk.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Fired != 0 {
		t.Fatalf("a disabled flow fired: %+v", res)
	}
}

// Manual and event flows are not the ticker's business; it must not fire them
// just because it is running.
func TestTheTickerOnlyFiresScheduleTriggers(t *testing.T) {
	when := at(t, "2026-08-04 09:00")
	fs, _, tk := newTestTicker(t, when)
	wireContent(t)
	f := goodFlow()
	f.Enabled, f.Mode = true, RunLive
	f.Trigger = Trigger{Kind: TriggerManual}
	if err := fs.Save(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	res, err := tk.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Fired != 0 {
		t.Fatalf("the ticker fired a manual flow: %+v", res)
	}
}

// A flow the ticker cannot consider is NAMED, not skipped. A flow an operator
// believes is armed and which silently never fires is the failure this whole
// design is trying to make impossible to have without noticing.
//
// Note precisely which path this exercises: the corrupted row is rejected by
// LoadableFlows (Flow.Complete parses the cron) and surfaces through the
// rejected map. It does NOT reach the ticker's own ParseCron branch, which is
// unreachable by construction — see the comment there, and the test below that
// pins the relationship keeping it unreachable.
func TestAFlowTheTickerCannotConsiderIsNamed(t *testing.T) {
	when := at(t, "2026-08-04 09:00")
	fs, _, tk := newTestTicker(t, when)
	wireContent(t)
	ctx := context.Background()
	f := scheduleFlow(t, fs, "0 9 * * *", RunLive)

	// Corrupt the stored expression the way a hand edit or a retired action
	// would — bypassing save-time validation entirely.
	if _, err := fs.db.ExecContext(ctx,
		`UPDATE vayuflow_flows SET trigger_cron=? WHERE id=?`, "not a cron", f.ID); err != nil {
		t.Fatal(err)
	}
	res, err := tk.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, named := res.Broken[f.ID]; !named {
		t.Fatalf("a broken schedule was skipped silently: %+v", res)
	}
}

// Save-time validation is what should make the above unreachable in practice.
func TestAFlowWithABadCronCannotBeSaved(t *testing.T) {
	fs := newTestStore(t)
	f := goodFlow()
	f.Trigger = Trigger{Kind: TriggerSchedule, Cron: "not a cron"}
	if err := fs.Save(context.Background(), &f); err == nil {
		t.Fatal("a flow with an unparseable cron expression was saved; it would never fire and " +
			"the operator would find out from the digest that did not arrive")
	}
}

// A dry-run schedule still fires — it just refuses at the effect boundary. An
// operator watching a dry run needs to see it happen on the real cadence.
func TestADryRunScheduleStillFires(t *testing.T) {
	when := at(t, "2026-08-04 09:00")
	fs, rs, tk := newTestTicker(t, when)
	fc := wireContent(t)
	ctx := context.Background()
	f := scheduleFlow(t, fs, "0 9 * * *", RunDryRun)

	if _, err := tk.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	runs, _ := rs.Recent(ctx, f.ID, 10)
	if len(runs) != 1 || runs[0].Status != StatusSucceeded {
		t.Fatalf("a dry-run schedule should still produce a run, got %+v", runs)
	}
	if created, _ := fc.counts(); created != 0 {
		t.Errorf("a dry-run schedule wrote %d drafts", created)
	}
	if runs[0].Steps[0].Refused == "" {
		t.Error("a dry-run firing must capture what it would have done")
	}
}

// The ticker's inner ParseCron error branch is unreachable only because
// save-time validation refuses everything the parser refuses. That is a
// relationship between two functions, and relationships drift — so it is
// pinned here rather than assumed.
//
// If this fails, the ticker's branch has become live and needs a real test.
func TestSaveTimeValidationIsWhatKeepsThisBranchUnreachable(t *testing.T) {
	bad := []string{
		"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *",
		"* * 0 * *", "* * 32 * *", "* * * 13 *", "* * * * 8",
		"a * * * *", "5-1 * * * *", "*/0 * * * *", "@daily", "MON * * * *",
	}
	for _, expr := range bad {
		if _, perr := ParseCron(expr); perr == nil {
			t.Fatalf("fixture %q parses; this test is checking the wrong expressions", expr)
		}
		tr := Trigger{Kind: TriggerSchedule, Cron: expr}
		if err := tr.Complete(); err == nil {
			t.Errorf("ParseCron refuses %q but Trigger.Complete accepts it — the ticker's "+
				"unreachable branch is now reachable and untested", expr)
		}
	}
}
