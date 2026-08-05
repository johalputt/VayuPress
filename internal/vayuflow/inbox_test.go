// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func newInboxRig(t *testing.T, role string) (*Inbox, *Store, *RunStore, *Drainer) {
	t.Helper()
	db := newTestDB(t)
	ib, fs, rs := NewInbox(db), NewStore(db), NewRunStore(db)
	rn := NewRunner(fs, rs, func(context.Context, string) (string, error) { return role, nil })
	return ib, fs, rs, NewDrainer(ib, fs, rn)
}

func eventFlow(t *testing.T, fs *Store, event string, mode RunMode) Flow {
	t.Helper()
	f := goodFlow()
	f.Enabled, f.Mode = true, mode
	f.Trigger = Trigger{Kind: TriggerEvent, Event: event}
	if err := fs.Save(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// THE P5 GATE. A crash between the event and the run must lose no run.
//
// The chain is durable at every link: the article and its outbox row commit
// together, the outbox marks a row delivered only after dispatch returns
// without error, and dispatch is what writes the inbox row. So a process that
// dies anywhere leaves the work recoverable — this test stands in for the last
// link by showing that a row appended and never drained is still there, and
// still fires, after a restart.
func TestAnUndrainedEventSurvivesARestart(t *testing.T) {
	ib, fs, rs, dr := newInboxRig(t, RoleAdmin)
	wireContent(t)
	ctx := context.Background()
	f := eventFlow(t, fs, EventArticleCreated, RunLive)

	if err := ib.Append(ctx, EventArticleCreated, "evt-1", Subject{Slug: "hello", Status: "draft"}); err != nil {
		t.Fatal(err)
	}
	// The process dies here — nothing drained it.
	pending, err := ib.PendingCount(ctx)
	if err != nil || pending != 1 {
		t.Fatalf("the event must survive undrained, got %d (%v)", pending, err)
	}
	// Restart: the drainer picks it up.
	res, err := dr.Drain(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Fired != 1 {
		t.Fatalf("the surviving event did not fire: %+v", res)
	}
	runs, _ := rs.Recent(ctx, f.ID, 10)
	if len(runs) != 1 || runs[0].Status != StatusSucceeded {
		t.Fatalf("expected one successful run, got %+v", runs)
	}
}

// The outbox may hand the same domain event over twice — a crash between
// dispatch and the delivered mark. That produces a second inbox row, and the
// run must still happen once per row rather than the flow running twice for one
// article. Both rows drain; the SECOND is a fresh identity, so it does run.
// What must NOT happen is the same row firing twice.
func TestARowIsNeverDrainedTwice(t *testing.T) {
	ib, fs, rs, dr := newInboxRig(t, RoleAdmin)
	wireContent(t)
	ctx := context.Background()
	f := eventFlow(t, fs, EventArticleCreated, RunLive)
	f.Budget.MaxRunsPerHour = 50
	if err := fs.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}

	if err := ib.Append(ctx, EventArticleCreated, "evt-1", Subject{}); err != nil {
		t.Fatal(err)
	}
	first, err := dr.Drain(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := dr.Drain(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fired != 1 || second.Rows != 0 {
		t.Fatalf("a drained row was offered again: first=%+v second=%+v", first, second)
	}
	runs, _ := rs.Recent(ctx, f.ID, 10)
	if len(runs) != 1 {
		t.Fatalf("one inbox row produced %d runs", len(runs))
	}
}

// A row nothing wants is still drained. Leaving it pending would make the inbox
// grow forever on an install with no event flows — the trigger-storm failure
// wearing a different hat.
func TestARowNoFlowWantsIsStillDrained(t *testing.T) {
	ib, _, _, dr := newInboxRig(t, RoleAdmin)
	ctx := context.Background()
	if err := ib.Append(ctx, EventArticleCreated, "evt-1", Subject{}); err != nil {
		t.Fatal(err)
	}
	res, err := dr.Drain(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.NoFlow != 1 {
		t.Errorf("expected the row to be counted as matching no flow, got %+v", res)
	}
	n, _ := ib.PendingCount(ctx)
	if n != 0 {
		t.Fatalf("a row no flow wanted stayed pending; the inbox would grow forever (%d pending)", n)
	}
}

// A storm must not grow the inbox without bound once drained rows are pruned,
// and it must not grow the RUN trail past the flow's hourly ceiling.
func TestAnEventStormIsBoundedByTheHourlyCeiling(t *testing.T) {
	ib, fs, rs, dr := newInboxRig(t, RoleAdmin)
	wireContent(t)
	ctx := context.Background()
	f := eventFlow(t, fs, EventArticleCreated, RunLive) // MaxRunsPerHour = 2

	for i := 0; i < 200; i++ {
		// Distinct envelope ids, as real events have — reusing one would be
		// deduped into a single row by the partial unique index, which is a
		// different property and is tested on its own below.
		if err := ib.Append(ctx, EventArticleCreated, fmt.Sprintf("evt-%d", i), Subject{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dr.Drain(ctx, 500); err != nil {
		t.Fatal(err)
	}
	runs, err := rs.Recent(ctx, f.ID, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) > f.Budget.MaxRunsPerHour {
		t.Fatalf("200 events produced %d runs against a ceiling of %d",
			len(runs), f.Budget.MaxRunsPerHour)
	}
	// Every row drained, so the inbox does not carry the storm forward.
	if n, _ := ib.PendingCount(ctx); n != 0 {
		t.Errorf("%d rows left pending after a drain pass", n)
	}
	// And pruning bounds the drained rows too.
	pruned, err := ib.PruneDrained(ctx, -time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 200 {
		t.Errorf("pruned %d of 200 drained rows", pruned)
	}
}

// Append must REFUSE an unknown event rather than storing a row nothing will
// ever drain into a run.
func TestAppendRefusesAnUnknownEvent(t *testing.T) {
	ib, _, _, _ := newInboxRig(t, RoleAdmin)
	err := ib.Append(context.Background(), "article.exploded", "evt", Subject{})
	if err == nil {
		t.Fatal("an unknown event was written to the inbox")
	}
	if !strings.Contains(err.Error(), "unknown event") {
		t.Errorf("the refusal should say what was wrong, got: %v", err)
	}
}

// And a flow cannot SUBSCRIBE to an event nothing publishes: it would sit armed
// and silent, which is the failure this subsystem refuses to allow unnoticed.
func TestAFlowCannotSubscribeToAnEventNothingPublishes(t *testing.T) {
	fs := newTestStore(t)
	f := goodFlow()
	f.Trigger = Trigger{Kind: TriggerEvent, Event: "article.exploded"}
	if err := fs.Save(context.Background(), &f); err == nil {
		t.Fatal("a flow subscribed to an event this install never publishes")
	}
}

// The subject travels from the event to the condition. A condition that never
// saw the subject would evaluate against an empty value and quietly hold or not
// hold for the wrong reason.
func TestTheEventSubjectReachesTheCondition(t *testing.T) {
	ib, fs, rs, dr := newInboxRig(t, RoleAdmin)
	wireContent(t)
	ctx := context.Background()

	f := goodFlow()
	f.Enabled, f.Mode = true, RunLive
	f.Trigger = Trigger{Kind: TriggerEvent, Event: EventArticleCreated}
	f.Condition = Condition{Kind: CondTagEquals, Value: "release"}
	if err := fs.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}

	// Does not carry the tag — must refuse.
	if err := ib.Append(ctx, EventArticleCreated, "e1", Subject{Tags: []string{"notes"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := dr.Drain(ctx, 10); err != nil {
		t.Fatal(err)
	}
	runs, _ := rs.Recent(ctx, f.ID, 10)
	if len(runs) != 1 || runs[0].Status != StatusRefused {
		t.Fatalf("a non-matching subject should refuse, got %+v", runs)
	}

	// Carries it — must run.
	if err := ib.Append(ctx, EventArticleCreated, "e2", Subject{Tags: []string{"release"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := dr.Drain(ctx, 10); err != nil {
		t.Fatal(err)
	}
	runs, _ = rs.Recent(ctx, f.ID, 10)
	if len(runs) != 2 {
		t.Fatalf("expected two runs, got %d", len(runs))
	}
	if runs[0].Status != StatusSucceeded {
		t.Fatalf("a matching subject should run, got %s (%s)", runs[0].Status, runs[0].Error)
	}
}

// A dry-run event flow still consumes its trigger and records what it would
// have done.
func TestADryRunEventFlowStillDrains(t *testing.T) {
	ib, fs, rs, dr := newInboxRig(t, RoleAdmin)
	fc := wireContent(t)
	ctx := context.Background()
	f := eventFlow(t, fs, EventArticleCreated, RunDryRun)

	if err := ib.Append(ctx, EventArticleCreated, "e1", Subject{}); err != nil {
		t.Fatal(err)
	}
	if _, err := dr.Drain(ctx, 10); err != nil {
		t.Fatal(err)
	}
	runs, _ := rs.Recent(ctx, f.ID, 10)
	if len(runs) != 1 || runs[0].Status != StatusSucceeded {
		t.Fatalf("a dry-run event flow should still produce a run, got %+v", runs)
	}
	if created, _ := fc.counts(); created != 0 {
		t.Errorf("a dry run wrote %d drafts", created)
	}
	if runs[0].Steps[0].Refused == "" {
		t.Error("the dry run must capture what it would have done")
	}
}

// Pruning must not remove work that has not been done.
func TestPruningNeverRemovesAnUndrainedRow(t *testing.T) {
	ib, _, _, _ := newInboxRig(t, RoleAdmin)
	ctx := context.Background()
	if err := ib.Append(ctx, EventArticleCreated, "e1", Subject{}); err != nil {
		t.Fatal(err)
	}
	pruned, err := ib.PruneDrained(ctx, -time.Hour) // a window that includes everything
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 {
		t.Fatalf("pruning removed %d undrained row(s); that is unrun work deleted", pruned)
	}
	if n, _ := ib.PendingCount(ctx); n != 1 {
		t.Errorf("the undrained row is gone (pending=%d)", n)
	}
}

// The outbox retries a FAILED dispatch by re-running the whole dispatch, so the
// same domain event can reach the inbox twice. Without deduplication that is two
// inbox rows, two identities, and two runs for one article — the duplicate
// delivery this engine exists to prevent, arriving through the back door.
func TestTheSameEventAppendedTwiceProducesOneRow(t *testing.T) {
	ib, fs, rs, dr := newInboxRig(t, RoleAdmin)
	wireContent(t)
	ctx := context.Background()
	f := eventFlow(t, fs, EventArticleCreated, RunLive)

	for i := 0; i < 3; i++ {
		if err := ib.Append(ctx, EventArticleCreated, "envelope-42", Subject{Slug: "a"}); err != nil {
			t.Fatalf("a repeat append must be accepted as already-recorded, not error: %v", err)
		}
	}
	n, err := ib.PendingCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("three appends of one envelope produced %d rows; a retried dispatch would run the flow %d times", n, n)
	}
	if _, err := dr.Drain(ctx, 10); err != nil {
		t.Fatal(err)
	}
	runs, _ := rs.Recent(ctx, f.ID, 10)
	if len(runs) != 1 {
		t.Fatalf("one article produced %d runs", len(runs))
	}
}

// A row with no envelope id must still be storable, and several of them must not
// collide with each other on the empty string — which is why the unique index is
// partial rather than plain.
func TestRowsWithNoEnvelopeIdDoNotCollide(t *testing.T) {
	ib, _, _, _ := newInboxRig(t, RoleAdmin)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := ib.Append(ctx, EventArticleUpdated, "", Subject{}); err != nil {
			t.Fatalf("append %d with no envelope id failed: %v", i, err)
		}
	}
	if n, _ := ib.PendingCount(ctx); n != 3 {
		t.Fatalf("three id-less events collapsed to %d rows; the unique index is not partial", n)
	}
}
