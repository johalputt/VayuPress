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
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	w.observe(now, 950*time.Millisecond, 3, false)
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
		w.observe(now.Add(time.Duration(i)*time.Second), 120*time.Millisecond, 4, false)
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
		w.observe(start.Add(time.Duration(i)*time.Second), 990*time.Millisecond, 10, false)
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
	w.observe(start.Add(7*time.Second), 5*time.Millisecond, 0, false)
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
		w.observe(start.Add(time.Duration(i)*time.Second), 4*time.Second, 4, false)
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
	w.observe(start.Add(1*time.Second), 950*time.Millisecond, 1, false)
	w.observe(start.Add(2*time.Second), 950*time.Millisecond, 1, false)
	if calls != 0 {
		t.Errorf("captured a snapshot after %v, below the %v threshold", 2*time.Second, w.dumpAfter)
	}

	// Past it: exactly one, and the event names it.
	w.observe(start.Add(3*time.Second), 950*time.Millisecond, 1, false)
	w.observe(start.Add(4*time.Second), 950*time.Millisecond, 1, false)
	w.observe(start.Add(5*time.Second), 950*time.Millisecond, 1, false)
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

	done := make(chan StallState, 1)
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
		last = persistStallDump("writestall", []byte("goroutine 1 [running]:\nmain.main()\n"))
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
	if p := persistStallDump("writestall", []byte("x")); p != "" {
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

	got := persistStallDump("writestall", []byte("goroutine 1 [running]:\n"))
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

// The read pool, under the fault it is watched for: every connection taken, a
// caller queued behind them. The watch must see it and take the snapshot while
// it lasts, and the snapshot must name the caller that is waiting. This runs
// the production sampler on the production pool accessor, against a real read
// pool, rather than feeding observe synthetic numbers.
func TestAReadPoolWithEveryConnectionTakenIsSeenAndSnapshotted(t *testing.T) {
	withTempDBPath(t)
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ClosePools)
	if RDB == nil {
		t.Fatal("no read pool opened for a file database; this test needs one")
	}

	w := &stallWatch{name: readStall.name, pool: readStall.pool, dumpAfter: 2 * time.Second}
	stop := make(chan struct{})
	w.start(stop)
	// As at boot: the watch has looked at the pool before it fills.
	time.Sleep(3 * stallTick)

	ctx, cancel := context.WithCancel(context.Background())
	var held []*sql.Conn
	t.Cleanup(func() {
		close(stop)
		cancel()
		for _, c := range held {
			_ = c.Close()
		}
	})
	for i := 0; i < RDB.Stats().MaxOpenConnections; i++ {
		c, err := RDB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	go waitsForAReadConnection(ctx)

	deadline := time.Now().Add(10 * time.Second)
	for {
		st := w.state()
		if st.Current != nil && st.Current.Dump != "" {
			if !strings.HasPrefix(filepath.Base(st.Current.Dump), "readstall-") {
				t.Errorf("the read pool's snapshot is %q; it must say which pool it is", st.Current.Dump)
			}
			b, err := os.ReadFile(st.Current.Dump)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), "waitsForAReadConnection") {
				t.Error("the snapshot does not show the caller queued for a read connection, so it cannot name what the site was waiting on")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("every read connection was taken and a caller queued for 10s, and the watch recorded %+v; "+
				"this is the 502 of 2026-09-26, unseen again", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitsForAReadConnection is a caller queued for the read pool, named so the
// snapshot can be searched for it.
func waitsForAReadConnection(ctx context.Context) {
	var n int
	_ = RDB.QueryRowContext(ctx, `SELECT 1`).Scan(&n)
}

// StartStallWatch, the call main makes, must start the read pool's watch as
// well as the writer's.
func TestStartStallWatchWatchesTheReadPool(t *testing.T) {
	withTempDBPath(t)
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ClosePools)
	t.Cleanup(func() {
		for _, s := range []*stallWatch{writeStall, readStall} {
			s.mu.Lock()
			s.started, s.dumper = false, nil
			s.mu.Unlock()
		}
	})
	// Already closed: each sampler starts and exits at its first select, so no
	// goroutine outlives the test to read a pool another test replaces.
	stop := make(chan struct{})
	close(stop)
	StartStallWatch(stop)

	if !ReadStall().Watching {
		t.Error("StartStallWatch did not start the read pool's watch; a read pool with every connection taken would go unseen")
	}
	if got := ReadStall().MaxOpen; got < 2 {
		t.Errorf("the read watch reports a pool of %d connection(s); it is sampling the writer, not the read pool", got)
	}
}

// Each pool keeps its own snapshots. A write stall recurring all afternoon
// must not prune away the only snapshot of the read stall before it.
func TestSnapshotsArePrunedPerPool(t *testing.T) {
	dir := t.TempDir()
	prev := config.Cfg.DBPath
	config.Cfg.DBPath = filepath.Join(dir, "vayupress.db")
	t.Cleanup(func() { config.Cfg.DBPath = prev })
	stalls := filepath.Join(dir, "stalls")
	if err := os.MkdirAll(stalls, 0o700); err != nil {
		t.Fatal(err)
	}
	// One read snapshot, then write snapshots past the cap.
	read := filepath.Join(stalls, "readstall-20000101T000000Z.txt")
	if err := os.WriteFile(read, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= stallDumpKeep+2; i++ {
		name := filepath.Join(stalls, "writestall-20000101T00000"+string(rune('0'+i))+"Z.txt")
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if persistStallDump("writestall", []byte("goroutine 1 [running]:\n")) == "" {
		t.Fatal("no snapshot written")
	}
	if _, err := os.Stat(read); err != nil {
		t.Error("write snapshots pruned away the read pool's only snapshot")
	}
	writes, _ := filepath.Glob(filepath.Join(stalls, "writestall-*.txt"))
	if len(writes) != stallDumpKeep {
		t.Errorf("%d write snapshots kept, want %d", len(writes), stallDumpKeep)
	}
}

// The looks within a second, one rule each.
func TestAPoolIsHeldOnlyWhenFullThroughoutWithACallerQueued(t *testing.T) {
	second := func(looks ...[2]int64) bool { // {full 0/1, WaitCount}
		h := newHeldPool(0)
		for _, l := range looks {
			h.look(int(l[0]), 1, l[1])
		}
		return h.second(looks[len(looks)-1][1])
	}
	// Not full at the last look of one second; full, with a caller queued in
	// between, at every look of the next.
	h := newHeldPool(0)
	h.look(0, 1, 0)
	h.second(0)
	h.look(1, 1, 1)
	h.look(1, 1, 1)
	if !h.second(1) {
		t.Error("a caller who queued as the pool filled, between two looks, was not counted")
	}
	h = newHeldPool(0)
	h.look(1, 1, 1)
	h.second(1)
	h.look(1, 1, 1)
	if !h.second(1) {
		t.Error("a pool full for a second second, its caller still queued, was not held")
	}
	if second([2]int64{1, 1}, [2]int64{0, 1}, [2]int64{1, 2}) {
		t.Error("a pool that emptied during the second was counted as held")
	}
	if second([2]int64{1, 0}, [2]int64{1, 0}) {
		t.Error("a full pool with nobody queued was counted as held")
	}
}

// A snapshot of a crowd must hold the whole crowd. runtime.Stack stops at the
// end of its buffer without saying so, and a read-pool stall is thousands of
// request goroutines: a dump cut off at a fixed size can end before the one
// goroutine that names the cause.
func TestASnapshotHoldsEveryGoroutine(t *testing.T) {
	dir := t.TempDir()
	prev := config.Cfg.DBPath
	config.Cfg.DBPath = filepath.Join(dir, "vayupress.db")
	t.Cleanup(func() { config.Cfg.DBPath = prev })

	const crowd = 8000 // well past the first buffer's megabyte
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(crowd)
	for i := 0; i < crowd; i++ {
		go parkedForTheSnapshot(&started, release)
	}
	started.Wait()
	path := goroutineDump("readstall")
	close(release)
	if path == "" {
		t.Fatal("no snapshot written")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(b), "db.parkedForTheSnapshot("); got < crowd {
		t.Errorf("the snapshot holds %d of %d parked goroutines (%d bytes); it was cut off", got, crowd, len(b))
	}
}

func parkedForTheSnapshot(started *sync.WaitGroup, release <-chan struct{}) {
	started.Done()
	<-release
}

// One caller stuck behind a full pool is a stall even though WaitDuration has
// not moved: database/sql adds a wait to it only when the wait ends.
func TestALoneCallerStuckBehindAFullPoolIsAStall(t *testing.T) {
	w := freshWatch(t)
	w.dumpAfter = 3 * time.Second
	w.dumper = func() string { return "/tmp/dump.txt" }
	start := time.Now()
	for i := 1; i <= 4; i++ {
		w.observe(start.Add(time.Duration(i)*time.Second), 0, 0, true)
	}
	if w.current == nil || w.current.Dump == "" {
		t.Fatalf("four seconds of a full pool with a caller queued were not a stall with a snapshot: %+v", w.current)
	}
}

// A pool with every connection in use and nobody queued is busy, not stalled.
// Real pool, production sampler.
func TestAFullPoolWithNobodyWaitingIsNotAStall(t *testing.T) {
	withTempDBPath(t)
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ClosePools)
	w := &stallWatch{name: readStall.name, pool: readStall.pool, dumpAfter: time.Second}
	stop := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	var held []*sql.Conn
	t.Cleanup(func() {
		close(stop)
		cancel()
		for _, c := range held {
			_ = c.Close()
		}
	})
	for i := 0; i < RDB.Stats().MaxOpenConnections; i++ {
		c, err := RDB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	w.start(stop)
	time.Sleep(3500 * time.Millisecond)
	if st := w.state(); st.Total != 0 {
		t.Errorf("a full pool with nobody queued was recorded as a stall: %+v", st)
	}
}
