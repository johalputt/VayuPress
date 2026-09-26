// SPDX-License-Identifier: Apache-2.0

package main

// vayukeep_integration.go — wiring VayuKeep replication into the app (ADR-0145).
//
// The engine is deliberately dependency-injected rather than reaching into the
// app: it takes a snapshot function, a verifier, a pressure signal and a clock,
// so internal/vayukeep needs no SQL driver, no HTTP, and no knowledge of
// VayuShield. That keeps the data-integrity-critical code testable in isolation,
// which for a subsystem the constitution rates Absolute (1.0) is the point.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/pace"
	"github.com/johalputt/vayupress/internal/pacedio"
	"github.com/johalputt/vayupress/internal/vayukeep"
)

// bootVayuKeep constructs and starts the replication engine. Replication is off
// unless VAYUKEEP_TARGET names somewhere to replicate to.
//
// A configuration error is fatal to the SUBSYSTEM, never to the site: the engine
// refuses to start and the reason is logged and surfaced on the operations page.
// Publishing must not depend on a backup target being reachable.
func (a *App) bootVayuKeep(ctx context.Context) {
	// One path in and out. Boot and the console both call applyKeepConfig, so a
	// setting changed from VayuOS produces exactly the engine a restart would.
	_ = a.applyKeepConfig(ctx)
}

// vayuKeepVerifier is the database half of the restore drill: it opens the
// database inside a restored generation and asks SQLite whether the pages are
// intact, then counts a table that must exist.
//
// Both halves matter. integrity_check alone passes on an empty but well-formed
// database, which is exactly what a restore that silently produced nothing looks
// like; the row count is what distinguishes "restored" from "restored something".
func (a *App) vayuKeepVerifier(ctx context.Context, dbPath string) (int64, error) {
	return verifyRestoredDB(ctx, dbPath, pace.Host().NewJob(pace.JobConfig{Min: 1, Max: 1, Floor: true}))
}

// verifyRestoredDB runs integrity_check one table at a time, asking the pacer
// before each, so the check of a 17 GB restore yields to visitors between
// tables instead of reading the whole file in one statement. A table's check
// covers its rows and every index on it. What it leaves out is the file-wide
// page accounting (a page claimed twice, or by nothing), which wastes space but
// loses no row. A single very large table is still one statement; that limit
// is recorded, not hidden.
func verifyRestoredDB(ctx context.Context, dbPath string, p pacedio.Pacer) (int64, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return 0, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type = 'table' ORDER BY name`)
	if err != nil {
		return 0, fmt.Errorf("the restored database's tables could not be listed: %w", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return 0, err
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, t := range tables {
		if _, err := p.Next(ctx); err != nil {
			return 0, err
		}
		var verdict string
		q := `PRAGMA integrity_check("` + strings.ReplaceAll(t, `"`, `""`) + `")`
		if err := db.QueryRowContext(ctx, q).Scan(&verdict); err != nil {
			return 0, fmt.Errorf("integrity_check of %s did not run: %w", t, err)
		}
		if verdict != "ok" {
			return 0, fmt.Errorf("integrity_check of %s reported %q", t, verdict)
		}
	}
	var n int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM articles`).Scan(&n); err != nil {
		return 0, fmt.Errorf("the restored database has no readable articles table: %w", err)
	}
	return n, nil
}

// vayuKeepStatus returns the current replication state, safe on a nil engine.
func (a *App) vayuKeepStatus() vayukeep.Status {
	if a.vayuKeep == nil {
		return vayukeep.Status{}
	}
	return a.vayuKeep.Status()
}

// vayuKeepPreflight takes a generation before a destructive operation and waits
// briefly for it, so an operator who runs a migration or an update has a
// restore point from immediately before it rather than from whenever the
// cadence last fired. It never blocks the operation for long, and never fails it.
func (a *App) vayuKeepPreflight(reason string) {
	if a.vayuKeep == nil || !config.Cfg.VayuKeepEnabled {
		return
	}
	logging.LogInfo("vayukeep", "pre-flight generation requested before "+reason)
	a.vayuKeep.TriggerNow()
}
