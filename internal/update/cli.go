package update

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/mode"
)

const (
	defaultOwner = "johalputt"
	defaultRepo  = "vayupress"
)

// RunCLI handles `vayupress update <check|apply|history>` invoked from main.
// It writes human-readable output to w. It enforces all safety gates for apply
// via PreflightApply before calling ApplyVerified.
func RunCLI(ctx context.Context, args []string, w io.Writer, db *sql.DB, current string) error {
	if len(args) == 0 {
		fmt.Fprintln(w, "usage: vayupress update <check|apply [--dry-run]|rollback|history>")
		return fmt.Errorf("update: missing subcommand")
	}

	owner, repo := defaultOwner, defaultRepo
	client := &http.Client{Timeout: 30 * time.Second}
	st := New(db)

	switch args[0] {
	case "check":
		return runCheck(ctx, w, client, owner, repo, st, current)
	case "apply":
		dryRun := false
		for _, a := range args[1:] {
			if a == "--dry-run" {
				dryRun = true
			}
		}
		return runApply(ctx, w, client, owner, repo, st, current, dryRun)
	case "rollback":
		return runRollback(ctx, w, st, current)
	case "history":
		return runHistory(ctx, w, st)
	default:
		return fmt.Errorf("update: unknown subcommand %q", args[0])
	}
}

// runRollback restores the previous binary kept as <binary>.bak by a prior
// apply, swapping it back over the running binary. The operator restarts after.
func runRollback(ctx context.Context, w io.Writer, st *Store, current string) error {
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("update: locate running binary: %w", err)
	}
	bak := binPath + ".bak"
	if _, err := os.Stat(bak); err != nil {
		return fmt.Errorf("update: no rollback artifact found at %s (nothing to roll back)", bak)
	}
	id, _ := st.Log(ctx, Record{ToVersion: current, Status: "started", Detail: "rollback"})
	if err := os.Rename(bak, binPath); err != nil {
		if id > 0 {
			_ = st.MarkComplete(ctx, id, "failed", "rollback: "+err.Error())
		}
		return fmt.Errorf("update: rollback swap failed: %w", err)
	}
	if id > 0 {
		_ = st.MarkComplete(ctx, id, "success", "rolled back from "+current)
	}
	fmt.Fprintf(w, "Rolled back to the previous binary (%s restored).\nRestart the service to run it.\n", bak)
	return nil
}

func runCheck(ctx context.Context, w io.Writer, client *http.Client, owner, repo string, st *Store, current string) error {
	rel, err := CheckLatest(ctx, client, owner, repo)
	if err != nil {
		return err
	}
	available := UpdateAvailable(current, rel.Version)

	detail := fmt.Sprintf("current=%s latest=%s available=%t", current, rel.Version, available)
	_, _ = st.Log(ctx, Record{FromVersion: current, ToVersion: rel.Version, Status: "checked", Detail: detail})

	fmt.Fprintf(w, "Current version : %s\n", current)
	fmt.Fprintf(w, "Latest version  : %s\n", rel.Version)
	fmt.Fprintf(w, "Update available: %t\n", available)
	if rel.URL != "" {
		fmt.Fprintf(w, "Release URL     : %s\n", rel.URL)
	}
	if rel.Notes != "" {
		fmt.Fprintf(w, "\nChangelog:\n%s\n", rel.Notes)
	}
	return nil
}

func runApply(ctx context.Context, w io.Writer, client *http.Client, owner, repo string, st *Store, current string, dryRun bool) error {
	enabled := os.Getenv("VAYU_SELFUPDATE_ENABLED") == "true"
	pubKey := os.Getenv("VAYU_RELEASE_PUBKEY")
	currentMode := string(mode.Global.Current())

	if err := PreflightApply(enabled, currentMode, pubKey); err != nil {
		_, _ = st.Log(ctx, Record{FromVersion: current, Status: "failed", Detail: "preflight: " + err.Error()})
		return err
	}

	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("update: locate running binary: %w", err)
	}

	id, _ := st.Log(ctx, Record{FromVersion: current, Status: "started", Detail: fmt.Sprintf("dry_run=%t", dryRun)})

	opt := ApplyOptions{
		Current:    current,
		DryRun:     dryRun,
		PubKeyHex:  pubKey,
		DBPath:     config.Cfg.DBPath,
		BackupDir:  config.Cfg.CacheDir + "/update-backups",
		BinaryPath: binPath,
	}

	newVersion, err := ApplyVerified(ctx, client, owner, repo, opt, st)
	if err != nil {
		if id > 0 {
			_ = st.MarkComplete(ctx, id, "failed", err.Error())
		}
		return err
	}

	if dryRun {
		if id > 0 {
			_ = st.MarkComplete(ctx, id, "checked", "dry-run verification passed for "+newVersion)
		}
		fmt.Fprintf(w, "Dry-run OK: %s verified (checksum + Ed25519 signature). Binary NOT replaced.\n", newVersion)
		return nil
	}

	if id > 0 {
		_ = st.MarkComplete(ctx, id, "success", "applied "+newVersion)
	}
	fmt.Fprintf(w, "Applied %s.\n\n%s\n", newVersion, RestartInstructions(newVersion))
	return nil
}

func runHistory(ctx context.Context, w io.Writer, st *Store) error {
	recs, err := st.List(ctx, 20)
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		fmt.Fprintln(w, "No update history.")
		return nil
	}
	fmt.Fprintf(w, "%-4s %-10s %-10s %-12s %s\n", "ID", "FROM", "TO", "STATUS", "STARTED")
	for _, r := range recs {
		fmt.Fprintf(w, "%-4d %-10s %-10s %-12s %s\n",
			r.ID, dash(r.FromVersion), dash(r.ToVersion), r.Status,
			r.StartedAt.Format(time.RFC3339))
	}
	return nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
