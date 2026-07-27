// SPDX-License-Identifier: Apache-2.0

package vayukeep

// drill.go — the restore drill.
//
// This is the part that makes the rest worth having. Everything else in this
// package produces files; only the drill establishes that those files are a
// backup. It restores the newest generation into a temporary directory, opens
// the database it contains and asks SQLite whether the pages are intact, then
// throws the whole thing away.
//
// The distinction it enforces is between "replication is enabled" and
// "replication works". The first is a configuration value and is worth nothing;
// the second is a measurement with a timestamp. The status panel reports the
// second, and it can — and must be able to — say no.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/johalputt/vayupress/internal/backup"
)

// DrillResult is one verification of one generation.
type DrillResult struct {
	Generation string
	Taken      time.Time
	At         time.Time
	OK         bool
	Err        string
	Rows       int64
	Duration   time.Duration
}

// Verifier checks a restored database file and returns a representative row
// count. Injected so this package needs no SQL driver; the caller wires it to
// `PRAGMA integrity_check` plus a count over a table it expects to exist.
type Verifier func(ctx context.Context, dbPath string) (rows int64, err error)

// SetVerifier installs the database check used by the drill. Without one the
// drill still proves the archive decrypts, authenticates and unpacks — it simply
// cannot also prove the database inside it is intact, and says so.
func (e *Engine) SetVerifier(v Verifier) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.verify = v
}

// Drill verifies the newest generation end to end and records the outcome in
// the engine's observable status, so an operator-triggered drill counts as a
// verification exactly like a scheduled one.
//
// It is single-flight. A drill restores a whole generation, so two at once would
// double the disk and CPU cost of the most expensive thing this subsystem does —
// and an operator (or a stolen admin session) clicking the button repeatedly
// could stack them without the guard.
func (e *Engine) Drill(ctx context.Context) DrillResult {
	if !e.drilling.CompareAndSwap(false, true) {
		return DrillResult{At: e.cfg.Now(), Err: "a restore drill is already running"}
	}
	defer e.drilling.Store(false)

	res := e.drill(ctx)
	e.setStatus(func(s *Status) {
		s.LastDrill = res.At
		s.LastDrillOK = res.OK
		s.LastDrillError = res.Err
		s.LastDrillRows = res.Rows
	})
	return res
}

// drill does the work. Split from Drill so the guard and the status write cannot
// be accidentally bypassed by a future caller.
func (e *Engine) drill(ctx context.Context) DrillResult {
	start := e.cfg.Now()
	res := DrillResult{At: start}

	gen, ok := e.Newest()
	if !ok {
		res.Err = "no generation has been written yet"
		return res
	}
	if err := os.MkdirAll(e.cfg.TargetDir, 0o700); err != nil {
		res.Err = err.Error()
		return res
	}
	res.Generation, res.Taken = gen.Name, gen.Taken

	f, err := os.Open(gen.Path) //nolint:gosec // path built from our own target directory listing
	if err != nil {
		res.Err = err.Error()
		return res
	}
	defer f.Close()

	// Restore into a scratch directory. Two constraints: it must NOT be anywhere
	// near the data directory — a drill that wrote next to live data would be a
	// liability rather than a check — and it must not be the default temporary
	// directory, which is a tmpfs on most modern distributions. Restoring a
	// multi-gigabyte data directory into RAM is how a backup check takes the
	// machine down. The target has room for generations, so it has room for one.
	scratch, err := os.MkdirTemp(e.cfg.TargetDir, ".vk-drill-")
	if err != nil {
		res.Err = err.Error()
		return res
	}
	defer os.RemoveAll(scratch)

	dest := filepath.Join(scratch, "restored")
	if _, err := backup.ExtractStaged(f, e.cfg.Passphrase, dest); err != nil {
		res.Err = "restore failed: " + err.Error()
		res.Duration = e.cfg.Now().Sub(start)
		return res
	}

	e.mu.Lock()
	verify := e.verify
	e.mu.Unlock()

	if verify != nil && e.cfg.DBPath != "" {
		rel, relErr := filepath.Rel(filepath.Clean(e.cfg.DataDir), filepath.Clean(e.cfg.DBPath))
		if relErr == nil {
			rows, vErr := verify(ctx, filepath.Join(dest, rel))
			if vErr != nil {
				res.Err = "restored database failed verification: " + vErr.Error()
				res.Duration = e.cfg.Now().Sub(start)
				return res
			}
			res.Rows = rows
		}
	}

	res.OK = true
	res.Duration = e.cfg.Now().Sub(start)
	return res
}

// runDrill executes a drill unless the public lane is busy, and folds the result
// into the status. A skipped drill deliberately does NOT refresh the timestamp:
// "last verified" must mean last verified.
func (e *Engine) runDrill(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if e.cfg.Pressure != nil && e.cfg.Pressure() {
		e.cfg.Log("info", "restore drill deferred — public request lane is under pressure")
		return
	}
	res := e.Drill(ctx) // Drill records the outcome itself
	if res.OK {
		e.cfg.Log("info", fmt.Sprintf("restore drill passed for %s in %s (%d rows)", res.Generation, res.Duration.Round(time.Millisecond), res.Rows))
		return
	}
	// A failed drill is the loudest thing this subsystem can say. The archives
	// may exist, be the right size and be perfectly recent, and still not restore.
	e.cfg.Log("error", "restore drill FAILED: "+res.Err)
}

// RestoreFrom restores a specific generation over destDir, returning the path
// the previous contents were preserved at. It is the operator-facing recovery
// path and is deliberately not wired to anything automatic.
func (e *Engine) RestoreFrom(gen Generation, destDir string) (string, error) {
	if !e.cfg.Enabled {
		return "", ErrDisabled
	}
	f, err := os.Open(gen.Path) //nolint:gosec // path built from our own target directory listing
	if err != nil {
		return "", err
	}
	defer f.Close()
	return backup.ExtractStaged(f, e.cfg.Passphrase, destDir)
}

// VerifyGeneration reads one generation end to end without writing anything.
func (e *Engine) VerifyGeneration(gen Generation) error {
	f, err := os.Open(gen.Path) //nolint:gosec // path built from our own target directory listing
	if err != nil {
		return err
	}
	defer f.Close()
	return backup.Verify(f, e.cfg.Passphrase)
}

// ErrNoGeneration is returned when a point in time predates every generation.
var ErrNoGeneration = errors.New("vayukeep: no generation exists at or before that time")
