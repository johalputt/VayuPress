// SPDX-License-Identifier: Apache-2.0

package vayukeep

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// restart builds a second engine over the same target, as an update or a
// settings change does, and runs its start-up.
func (h *harness) restart(t *testing.T) *Engine {
	t.Helper()
	e, err := New(h.engine.cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.Run(ctx)
	return e
}

// proveNewest writes a generation and passes a drill of it.
func proveNewest(t *testing.T, h *harness) (Generation, DrillResult) {
	t.Helper()
	h.engine.SetVerifier(corruptibleVerifier)
	h.engine.cycle(context.Background(), true)
	res := h.engine.Drill(context.Background())
	newest, _ := h.engine.Newest()
	if !res.OK || res.Generation != newest.Name {
		t.Fatalf("setup drill: %+v", res)
	}
	return newest, res
}

// On johal.in a 10.5 GiB restore point read "Test restore passed" before the
// update to 3.17.96 and "never restored" after it, with the newest backup
// "0 B" and the last successful write "never": all of it was held in memory.
func TestARestartKeepsTheTestRestore(t *testing.T) {
	h := newHarness(t, nil)
	newest, res := proveNewest(t, h)
	h.advance(time.Hour)

	e := h.restart(t)
	st := e.Status()
	if !st.LastDrillOK || !st.LastDrill.Equal(res.At) || st.LastDrillRows != 7 {
		t.Errorf("after a restart the test restore reads ok=%v at %v rows %d; want the pass at %v, 7 rows", st.LastDrillOK, st.LastDrill, st.LastDrillRows, res.At)
	}
	if e.ProvenGeneration() != newest.Name {
		t.Errorf("after a restart the proven restore point is %q, want %q", e.ProvenGeneration(), newest.Name)
	}
	if st.LastGenBytes != newest.Bytes {
		t.Errorf("after a restart the newest backup reads %d bytes, want %d", st.LastGenBytes, newest.Bytes)
	}
	if !st.LastSuccess.Equal(newest.Taken) {
		t.Errorf("after a restart the last successful write reads %v, want %v", st.LastSuccess, newest.Taken)
	}
	if !st.Healthy(h.now) {
		t.Error("a proven backup reads unhealthy after a restart")
	}
}

// The proof is about one file. A generation replaced under its own name, by
// size or only by its time, is an archive no drill has read.
func TestAReplacedBackupLosesItsProof(t *testing.T) {
	for _, c := range []struct {
		name    string
		replace func(t *testing.T, g Generation)
	}{
		{"size", func(t *testing.T, g Generation) {
			fi, _ := os.Stat(g.Path)
			if err := os.WriteFile(g.Path, []byte("another archive"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(g.Path, fi.ModTime(), fi.ModTime()); err != nil {
				t.Fatal(err)
			}
		}},
		{"time", func(t *testing.T, g Generation) {
			later := time.Now().Add(time.Hour)
			if err := os.Chtimes(g.Path, later, later); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, nil)
			newest, _ := proveNewest(t, h)
			c.replace(t, newest)
			e := h.restart(t)
			if p := e.ProvenGeneration(); p != "" {
				t.Errorf("a replaced restore point kept its proof (%s)", p)
			}
			if st := e.Status(); !st.LastDrill.IsZero() {
				t.Errorf("a pass on a replaced restore point was read back: ok=%v at %v", st.LastDrillOK, st.LastDrill)
			}
		})
	}
}

// A failed test restore is read back as failed. The proof of the older
// generation that passed before it is kept for retention, but the pass is not
// revived as the outcome.
func TestARestartKeepsAFailureAFailure(t *testing.T) {
	h := newHarness(t, nil)
	older, _ := proveNewest(t, h)
	h.advance(time.Hour)
	if err := os.WriteFile(h.dbPath, []byte("garbage"), 0o640); err != nil {
		t.Fatal(err)
	}
	h.engine.cycle(context.Background(), true)
	failed := h.engine.Drill(context.Background())
	if failed.OK {
		t.Fatal("setup: the drill of a corrupt database passed")
	}

	e := h.restart(t)
	st := e.Status()
	if st.LastDrillOK || st.LastDrillError != failed.Err || !st.LastDrill.Equal(failed.At) {
		t.Errorf("after a restart the failed test restore reads ok=%v err=%q at %v", st.LastDrillOK, st.LastDrillError, st.LastDrill)
	}
	if e.ProvenGeneration() != older.Name {
		t.Errorf("the proof of %s was not kept: %q", older.Name, e.ProvenGeneration())
	}
}

// Start-up reads the record, but a drill this process has already done is
// newer than anything in it: neither its outcome nor its proof is replaced.
func TestTheRecordNeverReplacesANewerDrill(t *testing.T) {
	h := newHarness(t, nil)
	proveNewest(t, h)
	old, err := os.ReadFile(filepath.Join(h.target, DrillRecordName))
	if err != nil {
		t.Fatal(err)
	}
	h.advance(time.Hour)
	h.engine.cycle(context.Background(), true)
	newer := h.engine.Drill(context.Background())
	if !newer.OK {
		t.Fatalf("setup: %+v", newer)
	}
	if err := os.WriteFile(filepath.Join(h.target, DrillRecordName), old, 0o600); err != nil {
		t.Fatal(err)
	}

	h.engine.loadDrillRecord()
	if st := h.engine.Status(); !st.LastDrill.Equal(newer.At) {
		t.Errorf("the record replaced this process's drill at %v with %v", newer.At, st.LastDrill)
	}
	if p := h.engine.ProvenGeneration(); p != newer.Generation {
		t.Errorf("the record replaced this process's proof %s with %s", newer.Generation, p)
	}
}

// Only a generation can be proven: retention cuts by comparing names, and a
// record naming any other file of the right size and time would move the cut.
func TestTheRecordProvesOnlyAGeneration(t *testing.T) {
	h := newHarness(t, nil)
	proveNewest(t, h)
	other := filepath.Join(h.target, "zz-not-a-backup")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(other)
	if err := h.engine.writeRecord(drillRecord{At: h.now, OK: true, Generation: "zz-not-a-backup",
		Proven: &provenOn{Name: "zz-not-a-backup", Bytes: fi.Size(), Modified: fi.ModTime().UnixNano()}}); err != nil {
		t.Fatal(err)
	}
	if p := h.restart(t).ProvenGeneration(); p != "" {
		t.Errorf("a record proved %q, which is not a generation", p)
	}
}

// Deleting the newest restore point by hand does not move "last successful
// write" back to the one before: the write still happened.
func TestTheLastWriteIsNotUndoneByADelete(t *testing.T) {
	h := newHarness(t, nil)
	h.engine.cycle(context.Background(), true)
	h.advance(time.Hour)
	h.engine.cycle(context.Background(), true)
	newest, _ := h.engine.Newest()
	if err := h.engine.Delete(newest); err != nil {
		t.Fatal(err)
	}
	if st := h.engine.Status(); !st.LastSuccess.Equal(h.now) {
		t.Errorf("after deleting the newest, the last successful write reads %v, want %v", st.LastSuccess, h.now)
	}
}

// The record is not a generation and not scratch: listing skips it and the
// sweep of abandoned partials leaves it, however old.
func TestTheRecordIsLeftAloneByListingAndSweep(t *testing.T) {
	h := newHarness(t, nil)
	proveNewest(t, h)
	rec := filepath.Join(h.target, DrillRecordName)
	old := h.now.Add(-48 * time.Hour)
	if err := os.Chtimes(rec, old, old); err != nil {
		t.Fatal(err)
	}
	h.advance(48 * time.Hour)
	if err := h.engine.prune(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rec); err != nil {
		t.Errorf("the sweep removed the drill record: %v", err)
	}
	gens, _ := h.engine.List()
	for _, g := range gens {
		if strings.Contains(g.Name, "drill") {
			t.Errorf("the record is listed as a generation: %s", g.Name)
		}
	}
}

// Before the first backup a drill has nothing to say: no record is written,
// and nothing is logged about failing to write one.
func TestADrillWithNoBackupRecordsNothing(t *testing.T) {
	var warned []string
	h := newHarness(t, func(c *Config) {
		c.Log = func(level, msg string) {
			if level == "warn" {
				warned = append(warned, msg)
			}
		}
	})
	if res := h.engine.Drill(context.Background()); res.OK {
		t.Fatal("setup: a drill with no backup passed")
	}
	if len(warned) > 0 {
		t.Errorf("a drill with no backup logged %q", warned)
	}
}
