// SPDX-License-Identifier: Apache-2.0

package vayukeep

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// copySnapshot stands in for `VACUUM INTO`. The engine takes the snapshot
// function as a dependency precisely so this package needs no SQL driver; the
// consistency of the real one is proved end to end in
// cmd/vayupress/backup_snapshot_test.go.
func copySnapshot(_ context.Context, dbPath, dest string) error {
	b, err := os.ReadFile(dbPath) //nolint:gosec // test fixture
	if err != nil {
		return err
	}
	return os.WriteFile(dest, b, 0o600)
}

type harness struct {
	engine  *Engine
	dataDir string
	dbPath  string
	target  string
	now     time.Time
	logs    []string
}

func newHarness(t *testing.T, mut func(*Config)) *harness {
	t.Helper()
	root := t.TempDir()
	h := &harness{
		dataDir: filepath.Join(root, "data"),
		target:  filepath.Join(root, "replica"),
		now:     time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
	}
	h.dbPath = filepath.Join(h.dataDir, "vayupress.db")
	if err := os.MkdirAll(h.dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.dbPath, []byte("SQLITE PAGES v1"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.dataDir, "settings.json"), []byte(`{"a":1}`), 0o640); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Enabled:    true,
		DataDir:    h.dataDir,
		DBPath:     h.dbPath,
		TargetDir:  h.target,
		Passphrase: "a test passphrase",
		Snapshot:   copySnapshot,
		Now:        func() time.Time { return h.now },
		Log:        func(level, msg string) { h.logs = append(h.logs, level+": "+msg) },
	}
	if mut != nil {
		mut(&cfg)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.engine = e
	return h
}

// advance moves the injected clock so generations get distinct names.
func (h *harness) advance(d time.Duration) { h.now = h.now.Add(d) }

// TestGenerationIsWrittenAndVerifies covers the happy path end to end: a
// generation is sealed onto the target and the drill restores it back.
func TestGenerationIsWrittenAndVerifies(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()

	h.engine.cycle(ctx, true)
	gens, err := h.engine.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(gens) != 1 {
		t.Fatalf("want 1 generation, got %d", len(gens))
	}
	// The archive must not contain plaintext from the data directory.
	raw, err := os.ReadFile(gens[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"SQLITE PAGES", "settings.json"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("generation leaks plaintext %q", leak)
		}
	}

	res := h.engine.Drill(ctx)
	if !res.OK {
		t.Fatalf("drill failed on a good generation: %s", res.Err)
	}
	if st := h.engine.Status(); st.Generations != 1 || st.NewestGen.IsZero() {
		t.Errorf("status not refreshed from target: %+v", st)
	}
}

// TestDrillFailsOnCorruptedGeneration is the test that gives the drill meaning.
// A check that cannot report failure is decoration; this corrupts the bytes on
// the target and requires the drill to notice.
func TestDrillFailsOnCorruptedGeneration(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.engine.cycle(ctx, true)

	gens, err := h.engine.List()
	if err != nil || len(gens) != 1 {
		t.Fatalf("setup: %v (%d generations)", err, len(gens))
	}
	raw, err := os.ReadFile(gens[0].Path)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("bit flip", func(t *testing.T) {
		bad := append([]byte{}, raw...)
		bad[len(bad)/2] ^= 0xFF
		if err := os.WriteFile(gens[0].Path, bad, 0o600); err != nil {
			t.Fatal(err)
		}
		if res := h.engine.Drill(ctx); res.OK {
			t.Fatal("drill passed on a generation with a flipped bit")
		}
	})

	t.Run("truncated", func(t *testing.T) {
		if err := os.WriteFile(gens[0].Path, raw[:len(raw)-32], 0o600); err != nil {
			t.Fatal(err)
		}
		if res := h.engine.Drill(ctx); res.OK {
			t.Fatal("drill passed on a truncated generation")
		}
	})

	t.Run("empty", func(t *testing.T) {
		if err := os.WriteFile(gens[0].Path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if res := h.engine.Drill(ctx); res.OK {
			t.Fatal("drill passed on an empty generation")
		}
	})
}

// TestDrillFailsWhenTheDatabaseInsideIsCorrupt covers the second half: the
// archive can be perfectly authentic and still contain an unusable database.
func TestDrillFailsWhenTheDatabaseInsideIsCorrupt(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.engine.SetVerifier(func(_ context.Context, dbPath string) (int64, error) {
		b, err := os.ReadFile(dbPath) //nolint:gosec // test fixture
		if err != nil {
			return 0, err
		}
		if !strings.HasPrefix(string(b), "SQLITE PAGES") {
			return 0, errors.New("integrity_check failed")
		}
		return 42, nil
	})

	h.engine.cycle(ctx, true)
	if res := h.engine.Drill(ctx); !res.OK || res.Rows != 42 {
		t.Fatalf("drill should pass and report rows, got ok=%v rows=%d err=%s", res.OK, res.Rows, res.Err)
	}

	// Now make the source database unusable and take a fresh generation.
	if err := os.WriteFile(h.dbPath, []byte("NOT A DATABASE"), 0o640); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	h.engine.cycle(ctx, true)
	res := h.engine.Drill(ctx)
	if res.OK {
		t.Fatal("drill passed on an authentic archive containing a corrupt database")
	}
	if !strings.Contains(res.Err, "verification") {
		t.Errorf("drill error should name the verification failure, got %q", res.Err)
	}
}

// TestHealthyRequiresAPassedDrill: having files is not the same as having a
// backup, and the status must not conflate them.
func TestHealthyRequiresAPassedDrill(t *testing.T) {
	h := newHarness(t, nil)
	h.engine.cycle(context.Background(), true)

	st := h.engine.Status()
	if st.Generations != 1 {
		t.Fatalf("setup: %d generations", st.Generations)
	}
	if st.Healthy(h.now) {
		t.Fatal("reported healthy with generations but no drill — 'enabled' is not 'working'")
	}
	h.engine.runDrill(context.Background())
	if !h.engine.Status().Healthy(h.now) {
		t.Fatal("still unhealthy after a passing drill")
	}
}

// TestSkippedDrillDoesNotRefreshTheTimestamp: deferring a drill under load must
// not make "last verified" newer than the last actual verification.
func TestSkippedDrillDoesNotRefreshTheTimestamp(t *testing.T) {
	busy := true
	h := newHarness(t, func(c *Config) { c.Pressure = func() bool { return busy } })
	ctx := context.Background()
	h.engine.cycle(ctx, true) // force bypasses pressure

	busy = false
	h.engine.runDrill(ctx)
	first := h.engine.Status().LastDrill
	if first.IsZero() {
		t.Fatal("drill did not run")
	}

	busy = true
	h.advance(time.Hour)
	h.engine.runDrill(ctx)
	if got := h.engine.Status().LastDrill; !got.Equal(first) {
		t.Errorf("a skipped drill advanced last-verified from %v to %v", first, got)
	}
}

// TestConfigRefusesUnsafeSetups — the engine must not start in a shape that
// silently produces a useless replica.
func TestConfigRefusesUnsafeSetups(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	base := Config{
		Enabled: true, DataDir: data, DBPath: filepath.Join(data, "x.db"),
		TargetDir: filepath.Join(root, "replica"), Passphrase: "pw", Snapshot: copySnapshot,
	}

	t.Run("target inside the data directory", func(t *testing.T) {
		c := base
		c.TargetDir = filepath.Join(data, "backups")
		if _, err := New(c); err == nil {
			t.Fatal("accepted a target inside the data directory — the copy would share the disk it insures against, and replicate itself")
		}
	})
	t.Run("target equal to the data directory", func(t *testing.T) {
		c := base
		c.TargetDir = data
		if _, err := New(c); err == nil {
			t.Fatal("accepted the data directory as its own target")
		}
	})
	t.Run("no passphrase", func(t *testing.T) {
		c := base
		c.Passphrase = ""
		if _, err := New(c); err == nil {
			t.Fatal("accepted an empty passphrase — generations are always encrypted")
		}
	})
	t.Run("no target", func(t *testing.T) {
		c := base
		c.TargetDir = ""
		if _, err := New(c); err == nil {
			t.Fatal("accepted an empty target")
		}
	})
	t.Run("remote target in Tor mode", func(t *testing.T) {
		c := base
		c.ClearnetBlocked = func() bool { return true }
		c.TargetDir = "sftp://backups.example.com/vayu"
		if _, err := New(c); err == nil {
			t.Fatal("accepted a remote target in Tor mode — that is a clearnet callback")
		}
		c.TargetDir = "backup@host:/vayu"
		if _, err := New(c); err == nil {
			t.Fatal("accepted an scp-style remote target in Tor mode")
		}
	})
	t.Run("local target in Tor mode is fine", func(t *testing.T) {
		c := base
		c.ClearnetBlocked = func() bool { return true }
		if _, err := New(c); err != nil {
			t.Fatalf("refused a local target in Tor mode: %v", err)
		}
	})
	t.Run("disabled config is never validated", func(t *testing.T) {
		c := base
		c.Enabled, c.Passphrase, c.TargetDir = false, "", ""
		if _, err := New(c); err != nil {
			t.Fatalf("a disabled engine must construct without configuration: %v", err)
		}
	})
}

// TestRetentionKeepsNewestOrRecent — either bound is enough to survive, so a
// quiet month cannot age out the only copy that exists.
func TestRetentionKeepsNewestOrRecent(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.RetainGenerations = 3
		c.RetainDays = 1
	})
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		h.engine.cycle(ctx, true)
		h.advance(6 * time.Hour) // 8 generations spanning two days
	}
	// Prune once more at the final clock. Each cycle above pruned against the
	// cutoff of its own moment, so asserting against the end-of-test cutoff
	// without this checks a state retention was never asked to produce.
	if err := h.engine.prune(); err != nil {
		t.Fatal(err)
	}
	gens, err := h.engine.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(gens) < 3 {
		t.Fatalf("retention kept %d generations, fewer than the %d floor", len(gens), 3)
	}
	cutoff := h.now.Add(-24 * time.Hour)
	for i, g := range gens {
		if i >= 3 && !g.Taken.After(cutoff) {
			t.Errorf("generation %s survived both bounds (position %d, taken %v)", g.Name, i, g.Taken)
		}
	}
}

// TestPointInTimeNeverRollsForward — restoring "as of" a moment must return the
// last state that existed BEFORE it. Returning the nearest generation in either
// direction would hand back exactly the data an operator is trying to escape.
func TestPointInTimeNeverRollsForward(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	var stamps []time.Time
	for i := 0; i < 4; i++ {
		h.engine.cycle(ctx, true)
		stamps = append(stamps, h.now)
		h.advance(time.Hour)
	}

	// Halfway between generation 1 and 2 must select generation 1.
	target := stamps[1].Add(30 * time.Minute)
	g, ok := h.engine.At(target)
	if !ok {
		t.Fatal("no generation found at or before a time inside the range")
	}
	if !g.Taken.Equal(stamps[1].Truncate(time.Second)) {
		t.Errorf("At(%v) chose %v, want %v — it must not roll forward", target, g.Taken, stamps[1])
	}
	// Before the first generation there is nothing to return.
	if _, ok := h.engine.At(stamps[0].Add(-time.Hour)); ok {
		t.Error("At() returned a generation taken after the requested time")
	}
	// After the last, the last is correct.
	g, ok = h.engine.At(h.now.Add(100 * time.Hour))
	if !ok || !g.Taken.Equal(stamps[3].Truncate(time.Second)) {
		t.Errorf("At() past the end chose %v, want the newest %v", g.Taken, stamps[3])
	}
}

// TestIdleInstallBacksOff — the adaptive cadence must actually stop working when
// nothing is happening, and must snap back the moment something does.
func TestIdleInstallBacksOff(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.MinInterval = time.Minute
		c.MaxInterval = 32 * time.Minute
	})
	ctx := context.Background()
	h.engine.cycle(ctx, true)
	if got := h.engine.nextInterval(); got != time.Minute {
		t.Fatalf("interval after activity = %v, want the minimum", got)
	}
	for i := 0; i < 10; i++ { // nothing changes on disk
		h.engine.cycle(ctx, false)
	}
	if got := h.engine.nextInterval(); got != 32*time.Minute {
		t.Errorf("idle interval = %v, want the maximum %v", got, 32*time.Minute)
	}
	gensBefore, _ := h.engine.List()

	// A write must bring it straight back to the fast cadence.
	h.advance(time.Minute)
	if err := os.WriteFile(h.dbPath, []byte("SQLITE PAGES v2 CHANGED"), 0o640); err != nil {
		t.Fatal(err)
	}
	h.engine.cycle(ctx, false)
	if got := h.engine.nextInterval(); got != time.Minute {
		t.Errorf("interval after a change = %v, want the minimum", got)
	}
	gensAfter, _ := h.engine.List()
	if len(gensAfter) != len(gensBefore)+1 {
		t.Errorf("a change produced %d new generations, want 1", len(gensAfter)-len(gensBefore))
	}
}

// TestCircuitBreakerPauses — a broken target must stop the engine loudly rather
// than turning into an endless retry against a wall.
func TestCircuitBreakerPauses(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.Snapshot = func(context.Context, string, string) error {
			return errors.New("no space left on device")
		}
	})
	ctx := context.Background()
	for i := 0; i < consecutiveFailuresToPause; i++ {
		h.advance(time.Minute)
		h.engine.cycle(ctx, true)
	}
	st := h.engine.Status()
	if !st.Paused {
		t.Fatalf("engine did not pause after %d failures: %+v", consecutiveFailuresToPause, st)
	}
	if st.LastError == "" || st.Healthy(h.now) {
		t.Errorf("a paused engine must report unhealthy with a reason: %+v", st)
	}
}

// TestPartialWritesAreNeverGenerations — an interrupted write must not leave
// something that looks restorable.
func TestPartialWritesAreNeverGenerations(t *testing.T) {
	h := newHarness(t, nil)
	if err := os.MkdirAll(h.target, 0o700); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(h.target, tmpPrefix+"123456")
	if err := os.WriteFile(partial, []byte("half an archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Also a plausible-looking file that is not ours at all.
	if err := os.WriteFile(filepath.Join(h.target, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	gens, err := h.engine.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(gens) != 0 {
		t.Fatalf("partial/foreign files were listed as generations: %+v", gens)
	}
	if _, ok := h.engine.Newest(); ok {
		t.Error("Newest() returned a partial write")
	}
}

// TestGenerationNameRoundTrip guards the only piece of state kept in a filename.
func TestGenerationNameRoundTrip(t *testing.T) {
	when := time.Date(2026, 7, 27, 8, 9, 10, 0, time.UTC)
	name := generationName(when)
	got, ok := parseGenerationName(name)
	if !ok || !got.Equal(when) {
		t.Fatalf("round trip failed: %s → %v ok=%v", name, got, ok)
	}
	for _, bad := range []string{"vk-.vpbk", "vk-notatime.vpbk", "other.vpbk", "vk-20260727-080910.txt", tmpPrefix + "x"} {
		if _, ok := parseGenerationName(bad); ok {
			t.Errorf("%q parsed as a generation", bad)
		}
	}
}

// ── Hardening regressions (found by an adversarial pass on this package) ─────

// TestDrillIsSingleFlight — a drill restores a whole generation. Two at once
// double the most expensive operation here, and an operator holding down the
// button (or a stolen admin session) could stack them indefinitely.
func TestDrillIsSingleFlight(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.engine.cycle(ctx, true)

	// Hold the guard as a concurrent drill would, then confirm a second caller is
	// turned away rather than starting a parallel restore.
	if !h.engine.drilling.CompareAndSwap(false, true) {
		t.Fatal("guard was already held")
	}
	res := h.engine.Drill(ctx)
	if res.OK {
		t.Fatal("a second concurrent drill ran instead of being refused")
	}
	if !strings.Contains(res.Err, "already running") {
		t.Errorf("refusal should say why, got %q", res.Err)
	}
	h.engine.drilling.Store(false)

	if res := h.engine.Drill(ctx); !res.OK {
		t.Fatalf("the guard was not released: %s", res.Err)
	}
}

// TestManualDrillRecordsItsResult — an operator who runs a drill and watches it
// pass must not then see "never verified" on the page.
func TestManualDrillRecordsItsResult(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.engine.cycle(ctx, true)

	if !h.engine.Status().LastDrill.IsZero() {
		t.Fatal("setup: a drill had already been recorded")
	}
	res := h.engine.Drill(ctx)
	if !res.OK {
		t.Fatalf("drill failed: %s", res.Err)
	}
	st := h.engine.Status()
	if st.LastDrill.IsZero() || !st.LastDrillOK {
		t.Errorf("a passing manual drill was not recorded in the status: %+v", st)
	}

	// And a failing manual drill must be recorded too — the page has to be able
	// to go from green to red without waiting for the scheduler.
	gens, _ := h.engine.List()
	if err := os.WriteFile(gens[0].Path, []byte("wreckage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := h.engine.Drill(ctx); res.OK {
		t.Fatal("drill passed on wreckage")
	}
	if st := h.engine.Status(); st.LastDrillOK {
		t.Error("a failing manual drill left the status green")
	}
}

// TestScratchSpaceStaysOffTheDefaultTempDir — /tmp is a tmpfs on most modern
// distributions, so snapshotting or restoring a multi-gigabyte data directory
// through it writes the whole thing to RAM and takes the machine down.
//
// Listing the temp directory afterwards proves nothing: both scratch paths are
// removed on the way out, so a wrong implementation looks identical to a right
// one. The paths are observed instead at the moment they are handed to the two
// callbacks that receive them.
func TestScratchSpaceStaysOffTheDefaultTempDir(t *testing.T) {
	var snapshotDest, restoredDB string
	h := newHarness(t, func(c *Config) {
		inner := c.Snapshot
		c.Snapshot = func(ctx context.Context, dbPath, dest string) error {
			snapshotDest = dest
			return inner(ctx, dbPath, dest)
		}
	})
	h.engine.SetVerifier(func(_ context.Context, dbPath string) (int64, error) {
		restoredDB = dbPath
		return 1, nil
	})
	ctx := context.Background()
	h.engine.cycle(ctx, true)
	if res := h.engine.Drill(ctx); !res.OK {
		t.Fatalf("drill failed: %s", res.Err)
	}

	// "Not under os.TempDir()" cannot be the assertion: the test's own scratch
	// tree lives there, so it would fail correct code. "Under the target" is the
	// property that actually matters and it excludes the default temp dir by
	// construction.
	for _, c := range []struct{ what, path string }{
		{"the database snapshot", snapshotDest},
		{"the drill restore", restoredDB},
	} {
		if c.path == "" {
			t.Fatalf("%s path was never observed — the test is not measuring anything", c.what)
		}
		if !strings.HasPrefix(c.path, h.target+string(os.PathSeparator)) {
			t.Errorf("%s must live on the target filesystem (%s), got %s", c.what, h.target, c.path)
		}
	}
}

// TestRetentionSweepsAbandonedScratch — an interrupted snapshot or drill leaves
// a directory on the target; it is never a generation, so it only costs disk.
func TestRetentionSweepsAbandonedScratch(t *testing.T) {
	h := newHarness(t, nil)
	if err := os.MkdirAll(h.target, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(h.target, ".vk-drill-abandoned")
	if err := os.MkdirAll(filepath.Join(stale, "restored"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := h.now.Add(-3 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := h.engine.prune(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("abandoned scratch space survived retention")
	}
}

// TestDeleteAndPruneAreOperatorControls — housekeeping has to be usable from the
// console, and Delete must actually free the space it claims to.
func TestDeleteAndPruneAreOperatorControls(t *testing.T) {
	// Generous limits during setup: tight ones would prune the fixtures away
	// mid-loop and leave the test asserting against whatever survived.
	h := newHarness(t, func(c *Config) { c.RetainGenerations = 10; c.RetainDays = 30 })
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		h.engine.cycle(ctx, true)
		h.advance(12 * time.Hour)
	}
	gens, err := h.engine.List()
	if err != nil || len(gens) != 4 {
		t.Fatalf("setup: %v (%d generations, want 4)", err, len(gens))
	}

	target := gens[len(gens)-1] // the oldest
	if err := h.engine.Delete(target); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(target.Path); err == nil {
		t.Error("Delete left the file on disk")
	}
	after, _ := h.engine.List()
	if len(after) != len(gens)-1 {
		t.Errorf("after delete: %d generations, want %d", len(after), len(gens)-1)
	}
	// Status must follow, or the page keeps reporting a copy that is gone.
	if st := h.engine.Status(); st.Generations != len(after) {
		t.Errorf("status still reports %d generations, want %d", st.Generations, len(after))
	}

	// Everything left is inside both limits, so cleaning up must remove nothing.
	// A prune that deletes anyway is the more dangerous bug of the two.
	if err := h.engine.Prune(); err != nil {
		t.Fatalf("prune: %v", err)
	}
	pruned, _ := h.engine.List()
	if len(pruned) != len(after) {
		t.Errorf("prune removed %d restore points that were within both limits", len(after)-len(pruned))
	}
	if st := h.engine.Status(); st.Generations != len(pruned) {
		t.Errorf("status not refreshed after prune: %d vs %d", st.Generations, len(pruned))
	}
}
