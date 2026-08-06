// SPDX-License-Identifier: Apache-2.0

package db

// stall_test.go — the write connection jamming must leave evidence.
//
// The incident that produced this file: a live site returned 502 for minutes,
// recovered on its own, and left behind a running process, a healthy database,
// no restart, no OOM kill and nothing in the log. Every fact available said
// "fine". The one thing that would have named it — the queue in front of the
// single write connection — was never measured, so the only answer anybody
// could give was a hypothesis.
//
// These tests are about the evidence, not the fix. A stall can still happen:
// something can always take the writer for a while. What must never happen
// again is a stall that nobody can see.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// freshWatch returns an isolated watcher so tests do not share the package
// global's accumulated history.
func freshWatch(t *testing.T) *stallWatch {
	t.Helper()
	return &stallWatch{dumpAfter: 5 * time.Second}
}

// A sample interval spent entirely waiting IS the failure. One caller blocked
// for a whole second is already an outage in progress; waiting for a crowd to
// form would miss the case where the crowd is what the stall creates.
func TestOneFullyBlockedIntervalIsAStall(t *testing.T) {
	w := freshWatch(t)
	now := time.Now()

	w.observe(now, 950*time.Millisecond, 3)
	if w.current == nil {
		t.Fatal("a sample interval spent almost entirely waiting for the write connection was not " +
			"recorded as a stall; this is the signal the incident had and nobody could read")
	}
	if w.current.Waits != 3 {
		t.Errorf("recorded %d callers delayed, want 3", w.current.Waits)
	}
}

// Ordinary contention is not a stall. A busy install takes the write connection
// constantly and briefly; reporting that as an incident trains an operator to
// ignore the panel, which is worse than not having it.
func TestBriefContentionIsNotAStall(t *testing.T) {
	w := freshWatch(t)
	now := time.Now()
	for i := 0; i < 10; i++ {
		w.observe(now.Add(time.Duration(i)*time.Second), 120*time.Millisecond, 4)
	}
	if w.current != nil {
		t.Error("normal write contention was reported as a stall; a panel that cries wolf is a " +
			"panel nobody reads")
	}
	if w.total != 0 {
		t.Errorf("counted %d stalls from ordinary contention", w.total)
	}
}

// A stall has a beginning, an end and a duration, and it survives into the
// history so an operator can find it AFTER the site recovered — which is when
// they will actually go looking.
func TestAStallIsRecordedWithItsDurationAndSurvivesRecovery(t *testing.T) {
	w := freshWatch(t)
	start := time.Now()

	for i := 1; i <= 6; i++ {
		w.observe(start.Add(time.Duration(i)*time.Second), 990*time.Millisecond, 10)
	}
	if w.current == nil {
		t.Fatal("no stall in progress after six fully-blocked seconds")
	}
	if d := w.current.Duration; d < 5*time.Second {
		t.Errorf("six blocked seconds measured as %v", d)
	}
	// Blocked accumulates every sample's queued time. With ONE waiter it tracks
	// the wall clock and lands just under it (a fully-blocked second measures
	// slightly below a second), so the meaningful assertion is only that it was
	// recorded at all — see the multi-waiter case below for the property that
	// makes this field worth having.
	if w.current.Blocked == 0 {
		t.Error("the stall records no queued time, so it cannot say what the stall cost anyone")
	}

	// Recovery.
	w.observe(start.Add(7*time.Second), 5*time.Millisecond, 0)
	if w.current != nil {
		t.Error("the stall is still marked ongoing after contention cleared")
	}
	if len(w.recent) != 1 {
		t.Fatalf("history holds %d events, want 1 — a stall an operator cannot find afterwards is "+
			"a stall nobody can act on", len(w.recent))
	}
	if w.recent[0].Ongoing {
		t.Error("the archived stall still claims to be ongoing")
	}
	if w.longest < 5*time.Second {
		t.Errorf("longest stall recorded as %v", w.longest)
	}
}

// The number that distinguishes a bad stall from a catastrophic one: queued
// time summed ACROSS callers. Forty seconds of contention costing six minutes
// of queued callers is a different incident from forty seconds costing forty,
// and only this field can tell them apart.
func TestQueuedTimeAcrossCallersExceedsTheStallItself(t *testing.T) {
	w := freshWatch(t)
	start := time.Now()
	// Four callers queued throughout each second.
	for i := 1; i <= 5; i++ {
		w.observe(start.Add(time.Duration(i)*time.Second), 4*time.Second, 4)
	}
	if w.current == nil {
		t.Fatal("no stall recorded")
	}
	if w.current.Blocked <= w.current.Duration {
		t.Errorf("queued time %v does not exceed the %v stall, so the panel cannot distinguish one "+
			"caller waiting from a crowd waiting", w.current.Blocked, w.current.Duration)
	}
	if w.current.Waits != 20 {
		t.Errorf("recorded %d delayed callers, want 20", w.current.Waits)
	}
}

// A goroutine snapshot must be taken WHILE it is stuck. Afterwards the stacks
// show nothing, which is precisely why this class of fault survived so long:
// by the time anyone looked, there was nothing left to look at.
func TestAGoroutineSnapshotIsTakenDuringTheStallAndOnlyOnce(t *testing.T) {
	w := freshWatch(t)
	w.dumpAfter = 3 * time.Second
	calls := 0
	w.dumper = func() string { calls++; return "/tmp/dump.txt" }

	start := time.Now()
	// Under the threshold: nothing yet. A dump per blip would fill a disk.
	w.observe(start.Add(1*time.Second), 950*time.Millisecond, 1)
	w.observe(start.Add(2*time.Second), 950*time.Millisecond, 1)
	if calls != 0 {
		t.Errorf("captured a snapshot after %v, below the %v threshold", 2*time.Second, w.dumpAfter)
	}

	// Past it: exactly one, and the event names it.
	w.observe(start.Add(3*time.Second), 950*time.Millisecond, 1)
	w.observe(start.Add(4*time.Second), 950*time.Millisecond, 1)
	w.observe(start.Add(5*time.Second), 950*time.Millisecond, 1)
	if calls != 1 {
		t.Errorf("took %d snapshots of one stall; one is the evidence, more is a disk-filling loop", calls)
	}
	if w.current.Dump == "" {
		t.Error("a snapshot was taken and the event does not say where it went, so nobody can find it")
	}
}

// Reporting must not need the database. WriteStall is the one thing that has to
// keep working when nothing else can get a connection.
func TestWriteStallTakesNoConnection(t *testing.T) {
	prev := DB
	DB = nil // the harshest case: no pool at all
	t.Cleanup(func() { DB = prev })

	done := make(chan WriteStallState, 1)
	go func() { done <- WriteStall() }()
	select {
	case st := <-done:
		if st.Stalled {
			t.Error("reported a stall with no database open")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WriteStall blocked. It is the one call that must answer during the incident it " +
			"describes; if it needs a connection it is useless exactly when it is needed.")
	}
}

// Snapshots must not become a second outage. A stall that recurs every few
// minutes would otherwise write a megabyte each time, forever.
func TestSnapshotsAreCappedOnDisk(t *testing.T) {
	dir := t.TempDir()
	prev := config.Cfg.DBPath
	config.Cfg.DBPath = filepath.Join(dir, "vayupress.db")
	t.Cleanup(func() { config.Cfg.DBPath = prev })

	var last string
	for i := 0; i < stallDumpKeep+4; i++ {
		last = persistStallDump([]byte("goroutine 1 [running]:\nmain.main()\n"))
		if last == "" {
			t.Fatal("could not write a snapshot")
		}
		// The filename carries a whole-second timestamp; step past it so each
		// write lands on its own name rather than overwriting the last.
		time.Sleep(1100 * time.Millisecond)
	}
	ents, err := filepath.Glob(filepath.Join(dir, "stalls", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) > stallDumpKeep {
		t.Errorf("%d snapshots on disk, cap is %d; a recurring stall must not fill the disk and "+
			"turn a diagnostic into a second outage", len(ents), stallDumpKeep)
	}
	if !strings.Contains(last, "writestall-") {
		t.Errorf("snapshot path %q does not identify what it is", last)
	}
}

// With no cache directory configured — a CLI process, a test — capturing must
// decline rather than scatter files or panic.
func TestSnapshotDeclinesWithNoStateDir(t *testing.T) {
	prev := config.Cfg.DBPath
	config.Cfg.DBPath = ""
	t.Cleanup(func() { config.Cfg.DBPath = prev })
	if p := persistStallDump([]byte("x")); p != "" {
		t.Errorf("wrote a snapshot to %q with no state directory configured", p)
	}
}

// AUDIT FINDING. Snapshots must not live under the cache directory.
//
// Every vhost this product writes gives nginx a `root` on the cache directory so
// the ACME challenge can be served from disk. That location is narrow today, so
// the original placement was not reachable — but a goroutine dump names internal
// paths and functions, and its safety must not rest on the exact wording of a
// location block in a shell script that this package cannot see.
func TestSnapshotsAreNotWrittenWhereNginxServesFiles(t *testing.T) {
	base := t.TempDir()
	prevDB, prevCache := config.Cfg.DBPath, config.Cfg.CacheDir
	config.Cfg.DBPath = filepath.Join(base, "state", "vayupress.db")
	config.Cfg.CacheDir = filepath.Join(base, "cache")
	t.Cleanup(func() { config.Cfg.DBPath, config.Cfg.CacheDir = prevDB, prevCache })

	got := persistStallDump([]byte("goroutine 1 [running]:\n"))
	if got == "" {
		t.Fatal("no snapshot written")
	}
	if strings.HasPrefix(got, config.Cfg.CacheDir) {
		t.Errorf("the snapshot went to %q, under the cache directory nginx roots for the ACME "+
			"challenge. A diagnostic full of internal paths must not sit one location-block edit "+
			"away from being downloadable.", got)
	}
	if !strings.HasPrefix(got, filepath.Dir(config.Cfg.DBPath)) {
		t.Errorf("the snapshot went to %q, which is not the state directory beside the database", got)
	}
}
