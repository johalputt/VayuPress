// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newTestStore builds a store on the REAL migration file rather than a
// hand-written CREATE TABLE.
//
// A test schema typed out by hand is a second copy of the real one, and two
// copies drift. This repo has already paid for that exact shape of bug in a
// stylesheet — a checked-in artifact 6 KB behind the constant the binary
// served — so the schema under test is the schema that ships, read from disk.
// It also means a malformed migration fails here rather than at first boot.
// vayuflowMigrations are every migration this subsystem owns, applied in order.
// Adding one here is part of adding it to the repo — a migration the tests do
// not apply is a schema nothing proves works.
var vayuflowMigrations = []string{
	"085-vayuflow.up.sql",
	"086-vayuflow-runs.up.sql",
	"087-vayuflow-inbox.up.sql",
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var executed int
	for _, name := range vayuflowMigrations {
		b, err := os.ReadFile(filepath.Join("..", "db", "migrations", name)) // #nosec G304 -- fixed path inside this repository
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		// The runner executes ONE statement per physical line, so the test does
		// the same. Doing anything smarter here (splitting on semicolons, say)
		// would let a migration pass the test and fail the runner.
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "--") {
				continue
			}
			if _, err := db.Exec(line); err != nil {
				t.Fatalf("%s: migration line failed: %v\n  %s", name, err, line)
			}
			executed++
		}
	}
	if executed == 0 {
		t.Fatal("the migration files produced no statements; this test is proving nothing")
	}
	return db
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(newTestDB(t))
}

// goodFlow is a minimal flow that satisfies every contract. Tests mutate one
// field at a time from here, so a failure names exactly the field that was
// supposed to be required.
func goodFlow() Flow {
	return Flow{
		Name:    "Draft a weekly digest",
		Trigger: Trigger{Kind: TriggerSchedule, Cron: "0 9 * * 1"},
		Steps:   []Step{{Action: "content.draft.create", Params: map[string]string{"title": "Digest", "slug": "digest"}}},
		Budget: Budget{
			MaxStepsPerRun: 4, MaxRunsPerHour: 2, MaxWritesPerRun: 1,
			MaxEgressPerRun: 1, Timeout: 30 * time.Second,
		},
		Owner: "user-1",
		Mode:  RunDryRun,
	}
}

func TestAGoodFlowSaves(t *testing.T) {
	s := newTestStore(t)
	f := goodFlow()
	if err := s.Save(context.Background(), &f); err != nil {
		t.Fatalf("a fully specified flow must save: %v", err)
	}
	if f.ID == "" || f.Version != 1 {
		t.Errorf("save must assign an ID and version 1, got id=%q version=%d", f.ID, f.Version)
	}
}

// The P1 gate: a flow with any unset contract field fails to save.
//
// Each case removes exactly one answer from an otherwise-valid flow. The table
// is the test — a new contract field added to Flow without a row here is a
// field nothing proves is required.
func TestAFlowMissingAnyContractFieldCannotSave(t *testing.T) {
	cases := []struct {
		name string
		bend func(*Flow)
		want string
	}{
		{"no mode", func(f *Flow) { f.Mode = runUnset }, "dry run or live"},
		{"no owner", func(f *Flow) { f.Owner = "" }, "no owner"},
		{"no name", func(f *Flow) { f.Name = "" }, "no name"},
		{"no trigger kind", func(f *Flow) { f.Trigger = Trigger{} }, "what fires it"},
		{"schedule without cron", func(f *Flow) { f.Trigger = Trigger{Kind: TriggerSchedule} }, "cron expression"},
		{"event without type", func(f *Flow) { f.Trigger = Trigger{Kind: TriggerEvent} }, "event type"},
		{"no steps", func(f *Flow) { f.Steps = nil }, "no steps"},
		{"unregistered action", func(f *Flow) { f.Steps = []Step{{Action: "content.publish.now"}} }, "no registered capability"},
		{"zero budget", func(f *Flow) { f.Budget = Budget{} }, "positive ceiling"},
		{"no step ceiling", func(f *Flow) { f.Budget.MaxStepsPerRun = 0 }, "MaxStepsPerRun"},
		{"no runs ceiling", func(f *Flow) { f.Budget.MaxRunsPerHour = 0 }, "MaxRunsPerHour"},
		{"no writes ceiling", func(f *Flow) { f.Budget.MaxWritesPerRun = 0 }, "MaxWritesPerRun"},
		{"no egress ceiling", func(f *Flow) { f.Budget.MaxEgressPerRun = 0 }, "MaxEgressPerRun"},
		{"no timeout", func(f *Flow) { f.Budget.Timeout = 0 }, "Timeout"},
	}
	s := newTestStore(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := goodFlow()
			tc.bend(&f)
			err := s.Save(context.Background(), &f)
			if err == nil {
				t.Fatalf("a flow with %s was saved; the contract is not enforced", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal should name the missing answer %q, got: %v", tc.want, err)
			}
			if f.ID != "" {
				t.Errorf("a refused flow must not be assigned an ID, got %q", f.ID)
			}
		})
	}
}

// "Unlimited" is inexpressible by design — and that is only true if a very
// large number is not a synonym for it.
func TestUnlimitedIsNotExpressibleByAnyRoute(t *testing.T) {
	s := newTestStore(t)
	for _, tc := range []struct{ name, want string }{
		{"huge writes", "ceiling"},
		{"huge timeout", "ceiling"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := goodFlow()
			if tc.name == "huge writes" {
				f.Budget.MaxStepsPerRun = 1 << 30
				f.Budget.MaxWritesPerRun = 1 << 30
			} else {
				f.Budget.Timeout = 30 * 24 * time.Hour
			}
			if err := s.Save(context.Background(), &f); err == nil {
				t.Fatal("a ceiling large enough to mean 'no ceiling' was accepted")
			}
		})
	}
}

// A ceiling that cannot be reached is misleading in the audit trail, where it
// reads as headroom the flow never had.
func TestAnUnreachableCeilingIsRefused(t *testing.T) {
	s := newTestStore(t)
	f := goodFlow()
	f.Budget.MaxStepsPerRun = 2
	f.Budget.MaxWritesPerRun = 9
	err := s.Save(context.Background(), &f)
	if err == nil {
		t.Fatal("a writes ceiling above the step ceiling was accepted")
	}
	if !strings.Contains(err.Error(), "never be reached") {
		t.Errorf("the refusal should say why, got: %v", err)
	}
}

func TestSaveRoundTripsEveryField(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	f := goodFlow()
	f.Enabled = true
	f.Condition = Condition{Kind: CondAll, Sub: []Condition{
		{Kind: CondTagEquals, Value: "release"},
		{Kind: CondNot, Sub: []Condition{{Kind: CondStatusIs, Value: "published"}}},
	}}
	f.Steps = []Step{{Action: "content.draft.create", Params: map[string]string{"title": "Digest"}}}
	if err := s.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != f.Name || got.Owner != f.Owner || got.Mode != f.Mode || !got.Enabled {
		t.Errorf("scalar fields did not round-trip: %+v", got)
	}
	if got.Trigger != f.Trigger {
		t.Errorf("trigger did not round-trip: got %+v want %+v", got.Trigger, f.Trigger)
	}
	if got.Budget != f.Budget {
		t.Errorf("budget did not round-trip: got %+v want %+v", got.Budget, f.Budget)
	}
	if len(got.Steps) != 1 || got.Steps[0].Action != "content.draft.create" ||
		got.Steps[0].Params["title"] != "Digest" {
		t.Errorf("steps did not round-trip: %+v", got.Steps)
	}
	if got.Condition.Kind != CondAll || len(got.Condition.Sub) != 2 {
		t.Errorf("condition did not round-trip: %+v", got.Condition)
	}
	// The nested NOT must survive, or a condition tree silently loosens on
	// reload — a flow that fired on drafts would start firing on everything.
	if got.Condition.Sub[1].Kind != CondNot || len(got.Condition.Sub[1].Sub) != 1 {
		t.Errorf("nested condition did not round-trip: %+v", got.Condition.Sub[1])
	}
}

func TestEditingBumpsTheVersion(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	f := goodFlow()
	if err := s.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	f.Name = "Renamed"
	if err := s.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if f.Version != 2 {
		t.Errorf("an edit must bump the version, got %d", f.Version)
	}
	got, err := s.Get(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 || got.Name != "Renamed" {
		t.Errorf("the bumped version must be persisted, got version=%d name=%q", got.Version, got.Name)
	}
}

// A flow an operator believes is armed, which the runner quietly ignores, is
// worse than one that errors. LoadableFlows has to report the rejects.
func TestAnUnloadableFlowIsReportedNotSkipped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	f := goodFlow()
	f.Enabled = true
	if err := s.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	// Simulate the row being edited outside the type system — by hand, or by an
	// upgrade that retired an action name.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE vayuflow_flows SET steps_json=? WHERE id=?`,
		`[{"Action":"content.publish.now"}]`, f.ID); err != nil {
		t.Fatal(err)
	}
	ok, rejected, err := s.LoadableFlows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ok) != 0 {
		t.Errorf("a flow naming an unregistered action must not load, got %d loadable", len(ok))
	}
	if _, named := rejected[f.ID]; !named {
		t.Fatal("the rejected flow must be reported by ID, not silently dropped")
	}
}

func TestSetModeReturnsThePriorMode(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	f := goodFlow()
	if err := s.Save(ctx, &f); err != nil {
		t.Fatal(err)
	}
	prior, err := s.SetMode(ctx, f.ID, RunLive)
	if err != nil {
		t.Fatal(err)
	}
	// The prior mode is what makes the arming record meaningful: "armed" with
	// no from-state does not distinguish a first arming from a re-arming.
	if prior != RunDryRun {
		t.Errorf("SetMode must report the prior mode, got %s", prior)
	}
	got, err := s.Get(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != RunLive {
		t.Errorf("mode did not persist, got %s", got.Mode)
	}
	if _, err := s.SetMode(ctx, f.ID, runUnset); err == nil {
		t.Error("SetMode must refuse an unset mode")
	}
}
