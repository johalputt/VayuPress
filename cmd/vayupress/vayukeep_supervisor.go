// SPDX-License-Identifier: Apache-2.0

package main

// vayukeep_supervisor.go — configuring and running VayuKeep from VayuOS, with no
// terminal and no restart (ADR-0145).
//
// v3.15.80 took its whole configuration from environment variables, which meant
// "set up automatic backups" was really "SSH in, edit /etc/vayupress/env, restart
// the service". For the one subsystem an operator most needs to actually turn on,
// that is the wrong bar. The console now owns it end to end.
//
// Where the settings live is deliberate:
//
//   - The target directory is a normal setting, because it is not a secret and an
//     operator should be able to see and change it.
//   - The passphrase is a sealed credential in the same AES-256-GCM store that
//     holds every other secret this install keeps. It is never a setting, never
//     logged, and never sent back to the browser.
//
// Environment variables still work and still win, so an install that configures
// this by configuration management is unaffected — but nothing requires them.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/update"
	"github.com/johalputt/vayupress/internal/vayukeep"
)

// keepSupervisor owns the running engine so it can be replaced in place when the
// operator changes the configuration.
type keepSupervisor struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// resolveKeepTarget returns the configured backup target: the environment
// variable when set (so configuration management keeps winning), otherwise the
// value saved from the console.
func (a *App) resolveKeepTarget(ctx context.Context) string {
	if t := strings.TrimSpace(config.Cfg.VayuKeepTarget); t != "" {
		return t
	}
	if a.siteSettings == nil {
		return ""
	}
	return strings.TrimSpace(a.siteSettings.Get(ctx, settings.KeyVayuKeepTarget))
}

// resolveKeepPassphrase returns the backup passphrase: the environment variable
// when set, otherwise the sealed credential.
func (a *App) resolveKeepPassphrase(ctx context.Context) string {
	if p := strings.TrimSpace(os.Getenv("VAYU_BACKUP_PASSPHRASE")); p != "" {
		return p
	}
	if a.secrets == nil {
		return ""
	}
	secret, _ := a.secrets.ProviderSecret(ctx, secrets.ProviderVayuKeep)
	return strings.TrimSpace(secret)
}

// keepEnabled reports whether automatic backup should be running.
func (a *App) keepEnabled(ctx context.Context) bool {
	if os.Getenv("VAYUKEEP_OFF") == "true" {
		return false
	}
	if strings.TrimSpace(config.Cfg.VayuKeepTarget) != "" {
		return true // env-configured installs stay env-controlled
	}
	if a.siteSettings == nil {
		return false
	}
	return a.siteSettings.Get(ctx, settings.KeyVayuKeepEnabled) == "true"
}

// buildKeepConfig assembles the engine configuration from every source.
func (a *App) buildKeepConfig(ctx context.Context) vayukeep.Config {
	return vayukeep.Config{
		Enabled:           a.keepEnabled(ctx),
		DataDir:           filepath.Dir(config.Cfg.DBPath),
		DBPath:            config.Cfg.DBPath,
		TargetDir:         a.resolveKeepTarget(ctx),
		Passphrase:        a.resolveKeepPassphrase(ctx),
		MinInterval:       time.Duration(config.Cfg.VayuKeepMinMin) * time.Minute,
		MaxInterval:       time.Duration(config.Cfg.VayuKeepMaxMin) * time.Minute,
		DrillInterval:     time.Duration(config.Cfg.VayuKeepDrillMin) * time.Minute,
		RetainGenerations: config.Cfg.VayuKeepRetainGen,
		RetainDays:        config.Cfg.BackupRetainDays,
		Snapshot:          snapshotLiveDB,
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
}

// applyKeepConfig (re)builds the engine and restarts its loop. It is the single
// path by which VayuKeep starts, stops or changes — boot and the console both go
// through here, so there is no second code path that could behave differently.
//
// A configuration error stops replication and is recorded; it never returns an
// error to the caller's detriment or takes the site down. The console shows
// vayuKeepErr, so a refusal is visible rather than silent.
func (a *App) applyKeepConfig(ctx context.Context) error {
	a.keepSup.mu.Lock()
	defer a.keepSup.mu.Unlock()

	// Stop whatever is running first, so two engines never share a target.
	if a.keepSup.cancel != nil {
		a.keepSup.cancel()
		a.keepSup.cancel = nil
	}

	cfg := a.buildKeepConfig(ctx)
	eng, err := vayukeep.New(cfg)
	if err != nil {
		a.vayuKeep = nil
		a.vayuKeepErr = err.Error()
		logging.LogError("vayukeep", "replication NOT started — "+err.Error(),
			"the site is unaffected, but nothing is being backed up")
		return err
	}
	eng.SetVerifier(a.vayuKeepVerifier)
	a.vayuKeep = eng
	a.vayuKeepErr = ""
	if !cfg.Enabled {
		return nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	a.keepSup.cancel = cancel
	go eng.Run(runCtx)
	logging.LogInfo("vayukeep", "replication running — target "+cfg.TargetDir)
	return nil
}

// ErrKeepBadTarget is returned when the operator's chosen directory cannot be used.
var ErrKeepBadTarget = errors.New("that folder cannot be used")

// validateKeepTargetWritable confirms VayuPress can actually create and write
// files there. The systemd sandbox restricts where the service may write, so a
// path that looks fine can still be denied — and finding that out at the first
// scheduled backup, silently, is exactly the failure mode this whole subsystem
// exists to prevent. Better to refuse now, on screen, with the real reason.
func validateKeepTargetWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".vk-writetest-")
	if err != nil {
		return err
	}
	name := probe.Name()
	defer os.Remove(name)
	if _, err := probe.WriteString("ok"); err != nil {
		probe.Close()
		return err
	}
	return probe.Close()
}

// vayuKeepStageRestore unpacks a restore point, validates the database inside it,
// and stages that database for the boot path to swap in atomically.
//
// Validation before staging is the whole point. ApplyPendingRestore will happily
// rename whatever it finds over the live database, so handing it an unverified
// file would turn "restore" into "replace your site with something that might be
// rubble". It is checked here, while the current database is still intact and
// the operator can be told no.
func (a *App) vayuKeepStageRestore(ctx context.Context, gen vayukeep.Generation) (string, error) {
	if a.vayuKeep == nil {
		return "", vayukeep.ErrDisabled
	}
	scratch, err := os.MkdirTemp(filepath.Dir(config.Cfg.DBPath), ".vk-staging-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(scratch)

	dest := filepath.Join(scratch, "unpacked")
	if _, err := a.vayuKeep.RestoreFrom(gen, dest); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(filepath.Dir(config.Cfg.DBPath), config.Cfg.DBPath)
	if err != nil {
		return "", err
	}
	src := filepath.Join(dest, rel)
	if _, err := os.Stat(src); err != nil {
		return "", errors.New("that restore point contains no database")
	}
	if _, err := a.vayuKeepVerifier(ctx, src); err != nil {
		return "", err
	}

	// Same filesystem as the live database, so the boot-time swap is a rename.
	staged := config.Cfg.DBPath + update.PendingRestoreSuffix
	_ = os.Remove(staged)
	if err := os.Rename(src, staged); err != nil {
		return "", err
	}
	logging.LogInfo("vayukeep", "restore staged from "+gen.Name+" — applies on next start")
	return staged, nil
}
