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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/vayukeep"
)

// bootVayuKeep constructs and starts the replication engine. Replication is off
// unless VAYUKEEP_TARGET names somewhere to replicate to.
//
// A configuration error is fatal to the SUBSYSTEM, never to the site: the engine
// refuses to start and the reason is logged and surfaced on the operations page.
// Publishing must not depend on a backup target being reachable.
func (a *App) bootVayuKeep(ctx context.Context) {
	dataDir := filepath.Dir(config.Cfg.DBPath)
	cfg := vayukeep.Config{
		Enabled:           config.Cfg.VayuKeepEnabled,
		DataDir:           dataDir,
		DBPath:            config.Cfg.DBPath,
		TargetDir:         config.Cfg.VayuKeepTarget,
		Passphrase:        backupPassphraseFromEnv(),
		MinInterval:       time.Duration(config.Cfg.VayuKeepMinMin) * time.Minute,
		MaxInterval:       time.Duration(config.Cfg.VayuKeepMaxMin) * time.Minute,
		DrillInterval:     time.Duration(config.Cfg.VayuKeepDrillMin) * time.Minute,
		RetainGenerations: config.Cfg.VayuKeepRetainGen,
		RetainDays:        config.Cfg.BackupRetainDays,
		Snapshot:          snapshotLiveDB,
		// Replication yields to readers. The L0 sovereign lane already knows when
		// the public pool is filling up, so reuse that signal rather than inventing
		// a second notion of "busy" that could disagree with it.
		Pressure: func() bool {
			g := a.sovereign
			return g != nil && g.Inflight()*4 >= g.Cap()*3
		},
		ClearnetBlocked: func() bool { return config.Cfg.OnionMode },
		Log: func(level, msg string) {
			if level == "error" {
				logging.LogError("vayukeep", msg, "")
				return
			}
			logging.LogInfo("vayukeep", msg)
		},
	}

	eng, err := vayukeep.New(cfg)
	if err != nil {
		// Refusing is the correct outcome for a bad configuration, but it must be
		// impossible to miss: an operator who set a target believes they have
		// backups, and silence here is exactly how they would keep believing it.
		logging.LogError("vayukeep", "replication NOT started — "+err.Error(),
			"the site is unaffected, but nothing is being backed up")
		a.vayuKeepErr = err.Error()
		return
	}
	eng.SetVerifier(a.vayuKeepVerifier)
	a.vayuKeep = eng
	if config.Cfg.VayuKeepEnabled {
		go eng.Run(ctx)
	}
}

// backupPassphraseFromEnv reads the one secret VayuKeep needs. It is the same
// variable `vayupress backup` uses, so an operator keeps one passphrase and
// every generation is restorable with the ordinary CLI.
func backupPassphraseFromEnv() string {
	return strings.TrimSpace(os.Getenv("VAYU_BACKUP_PASSPHRASE"))
}

// vayuKeepVerifier is the database half of the restore drill: it opens the
// database inside a restored generation and asks SQLite whether the pages are
// intact, then counts a table that must exist.
//
// Both halves matter. integrity_check alone passes on an empty but well-formed
// database, which is exactly what a restore that silently produced nothing looks
// like; the row count is what distinguishes "restored" from "restored something".
func (a *App) vayuKeepVerifier(ctx context.Context, dbPath string) (int64, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return 0, fmt.Errorf("integrity_check did not run: %w", err)
	}
	if integrity != "ok" {
		return 0, fmt.Errorf("integrity_check reported %q", integrity)
	}
	var rows int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM articles`).Scan(&rows); err != nil {
		return 0, fmt.Errorf("the restored database has no readable articles table: %w", err)
	}
	return rows, nil
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
