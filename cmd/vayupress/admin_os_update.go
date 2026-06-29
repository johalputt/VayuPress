package main

// admin_os_update.go — VayuOS "Update & Backup" panel.
//
// This brings two operator capabilities that previously required shell access
// (CLI `vayupress update …`, manual file copies) into a single one-click admin
// surface, while preserving every security guarantee of the underlying engine:
//
//   1. One-click self-update. Check GitHub for a newer signed release, then
//      apply it — download, SHA-256 + Ed25519 signature verification against the
//      pinned release key, automatic database backup, atomic binary swap, and an
//      in-process re-exec to activate the new version. No command line, nothing
//      left half-done. Rollback restores the previous binary.
//
//   2. Full backup / export / import. Download the entire site (database +
//      every setting) as one consistent, checksummed .tar.gz, and restore from
//      such a file. Export and import stream with the server read/write
//      deadlines lifted, so there is no size limit — a multi-gigabyte site moves
//      in constant memory.
//
// Security posture: an apply is admin-role gated and CSRF-protected (an
// authenticated admin clicking Update is the explicit opt-in), and is refused in
// read-only/quarantined/maintenance modes. The downloaded release is ALWAYS
// SHA-256 checksum verified; if a release signing key is pinned
// (VAYU_RELEASE_PUBKEY) the Ed25519 signature is additionally required. Every
// action is recorded in the WORM audit log and the update_history table.
//
// CSP posture is identical to the rest of VayuOS: no inline styles, the only
// inline <script> carries the per-request nonce, all interpolated values are
// escaped, and DOM writes in the JS use textContent.

import (
	"encoding/json"
	"fmt"
	"html"
	htmpl "html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/update"
)

const (
	updateOwner = "johalputt"
	updateRepo  = "vayupress"
)

// restartCleanup flushes and closes the database immediately before a re-exec
// so a self-restart never loses a write or leaves the WAL un-checkpointed.
func restartCleanup() {
	if dbpkg.DB != nil {
		if _, err := dbpkg.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			logging.LogError("update", "pre-restart WAL checkpoint", err.Error())
		}
		_ = dbpkg.DB.Close()
	}
}

// selfUpdateConfigured reports whether the operator has opted in and pinned a
// release key, i.e. whether one-click apply can run.
func selfUpdateConfigured() (enabled bool, hasKey bool) {
	return os.Getenv("VAYU_SELFUPDATE_ENABLED") == "true", strings.TrimSpace(os.Getenv("VAYU_RELEASE_PUBKEY")) != ""
}

// binaryDirWritable returns an empty string when the directory holding binPath
// can be written (so the atomic binary swap can create its temp file there), or
// a short human reason when it cannot. It probes by actually creating and
// removing a temp file — the only reliable test across permission bits, a
// read-only mount, and a systemd ProtectSystem sandbox.
func binaryDirWritable(binPath string) string {
	dir := filepath.Dir(binPath)
	f, err := os.CreateTemp(dir, ".vayupress-write-probe-*")
	if err != nil {
		if os.IsPermission(err) {
			return "permission denied writing to " + dir + "."
		}
		msg := err.Error()
		if strings.Contains(msg, "read-only") {
			return dir + " is mounted read-only."
		}
		return "could not write to " + dir + " (" + msg + ")."
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return ""
}

// ── Page ─────────────────────────────────────────────────────────────────────

func (a *App) handleOSUpdate(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	_, hasKey := selfUpdateConfigured()
	curMode := string(mode.Global.Current())
	modeOK := update.PreflightMode(curMode) == nil

	// Pre-render a banner explaining the current verification posture. One-click
	// apply works for an authenticated admin; pinning a release key only upgrades
	// verification from checksum to checksum+signature.
	var banner string
	switch {
	case !modeOK:
		banner = `<div class="settings-callout">
    <strong>Updates are paused.</strong>
    <span class="text-sm muted">The system is in <code>` + html.EscapeString(curMode) + `</code> mode, which blocks changing the binary. Checking for updates and backups still work; updates resume automatically once the system returns to normal.</span>
  </div>`
	case hasKey:
		banner = `<div class="settings-callout">
    <strong>One-click updates are armed (signature-verified).</strong>
    <span class="text-sm muted">Releases are verified by SHA-256 checksum <em>and</em> Ed25519 signature against your pinned release key, then the binary is swapped atomically and the service re-launches itself to finish. A database backup before updating is optional (see the checkbox below).</span>
  </div>`
	default:
		banner = `<div class="settings-callout">
    <strong>One-click updates are ready.</strong>
    <span class="text-sm muted">Click <em>Update now</em> to install the latest release: the download is SHA-256 checksum verified, the binary is swapped atomically, and the service restarts. A pre-update database backup is optional (checkbox below). For an extra layer, pin a release signing key in <code>VAYU_RELEASE_PUBKEY</code> to also require Ed25519 signature verification.</span>
  </div>`
	}

	historyRows := a.updateHistoryRowsHTML(r)

	// Start disabled; the on-load check enables it only when an update is
	// actually available and the mode allows applying.
	applyDisabled := " disabled"

	body := `<div class="page-header">
  <h1>Update &amp; Backup</h1>
  <div class="page-actions">
    <span class="text-sm muted">Current version <strong>v` + html.EscapeString(Version) + `</strong> · mode <strong>` + html.EscapeString(curMode) + `</strong></span>
  </div>
</div>
` + banner + `

<div class="card mb-6" data-update-card>
  <div class="card-title">Software update</div>
  <p class="text-sm muted mb-4">Install the latest signed VayuPress release in one click — download, signature verification, automatic database backup, atomic swap and restart are all handled for you.</p>
  <div class="update-row" data-update-state>
    <div class="update-version">
      <div class="field-label">Installed</div>
      <div class="update-version__value">v` + html.EscapeString(Version) + `</div>
    </div>
    <div class="update-version">
      <div class="field-label">Latest release</div>
      <div class="update-version__value" data-latest-version>—</div>
    </div>
    <div class="update-version">
      <div class="field-label">Status</div>
      <div class="update-version__value" data-update-status>Not checked yet</div>
    </div>
  </div>
  <div class="update-notes" data-update-notes hidden></div>
  <label class="cz-check mt-4" style="justify-content:flex-start;gap:10px">
    <input type="checkbox" data-update-backup checked> Back up the database first
  </label>
  <div class="text-xs muted mb-2">Recommended for most sites. A binary update never changes your database and the previous binary is kept for rollback, so you can safely untick this — handy for very large databases where a full snapshot is slow. For a downloadable copy, use Export below.</div>
  <div class="theme-actions mt-2" data-actions-wrap>
    <button type="button" class="btn btn--ghost btn--sm" data-update-check>Check for updates</button>
    <button type="button" class="btn btn--primary btn--sm" data-update-apply` + applyDisabled + `>Update now</button>
    <button type="button" class="btn btn--ghost btn--sm" data-update-rollback>Roll back</button>
    <span class="text-xs muted" data-update-msg role="status" aria-live="polite"></span>
  </div>
</div>

<div class="card mb-6" data-backup-card>
  <div class="card-title">Backup &amp; restore</div>
  <p class="text-sm muted mb-4">Download your entire site — database and every setting — as a single consistent, checksummed archive. Restore it on this or another server. There is no size limit on export or import.</p>

  <div class="settings-block-title">Export</div>
  <p class="text-sm muted mb-2">Creates a point-in-time <code>.tar.gz</code> snapshot and downloads it to your computer.</p>
  <a class="btn btn--primary btn--sm" href="/os/api/backup/export" data-backup-export download>Download full backup</a>

  <div class="section-divider mt-4"></div>

  <div class="settings-block-title mt-4">Import / restore</div>
  <p class="text-sm muted mb-2">Restores a previously exported snapshot. Your current database is automatically backed up first, then the service restarts to load the restored data. <strong>This replaces all current content and settings.</strong></p>
  <div class="theme-actions" data-restore-wrap>
    <input type="file" id="backup-file" class="input" accept=".gz,.tgz,application/gzip,application/x-gzip" data-backup-file style="max-width:22rem">
    <button type="button" class="btn btn--danger btn--sm" data-backup-import>Restore from file</button>
    <span class="text-xs muted" data-backup-msg role="status" aria-live="polite"></span>
  </div>
  <div class="progress mt-3" data-restore-progress hidden><div class="progress__bar progress__bar--ok w-0" data-restore-bar></div></div>
</div>

<div class="card" data-history-card>
  <div class="card-title">Update history</div>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>#</th><th>From</th><th>To</th><th>Status</th><th>Detail</th><th>When</th></tr></thead>
    <tbody data-history-body>` + historyRows + `</tbody>
  </table></div>
</div>

<script nonce="` + nonce + `" src="/os/static/js/admin-os-update.js?v=` + assetVer("js/admin-os-update.js") + `"></script>`

	writeOSHTML(w, adminOSLayout(nonce, "Update & Backup", "update", cfg, htmpl.HTML(body)))
}

// updateHistoryRowsHTML renders the most recent update_history rows as table
// rows (escaped). Returns an empty-state row when there is no history.
func (a *App) updateHistoryRowsHTML(r *http.Request) string {
	if a.updateStore == nil {
		return `<tr><td colspan="6" class="muted">Update history unavailable.</td></tr>`
	}
	recs, err := a.updateStore.List(r.Context(), 25)
	if err != nil || len(recs) == 0 {
		return `<tr><td colspan="6" class="muted">No update activity recorded yet.</td></tr>`
	}
	var b strings.Builder
	for _, rec := range recs {
		when := rec.StartedAt.UTC().Format("2 Jan 2006 15:04")
		b.WriteString(`<tr>
  <td class="muted">` + strconv.FormatInt(rec.ID, 10) + `</td>
  <td>` + html.EscapeString(dashOr(rec.FromVersion)) + `</td>
  <td>` + html.EscapeString(dashOr(rec.ToVersion)) + `</td>
  <td>` + updateStatusPill(rec.Status) + `</td>
  <td class="muted text-sm">` + html.EscapeString(rec.Detail) + `</td>
  <td class="muted text-sm">` + html.EscapeString(when) + `</td>
</tr>`)
	}
	return b.String()
}

func updateStatusPill(status string) string {
	cls := "status-pill"
	switch status {
	case "success":
		cls = "status-pill status-pill--live"
	case "failed":
		cls = "status-pill status-pill--draft"
	}
	return `<span class="` + cls + `">` + html.EscapeString(status) + `</span>`
}

func dashOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// ── JSON APIs ─────────────────────────────────────────────────────────────────

// handleOSUpdateCheck queries GitHub for the latest release (read-only). It
// records the check in update_history, matching the CLI `update check`.
func (a *App) handleOSUpdateCheck(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{Timeout: 30 * time.Second, Transport: safeOutboundTransport()}
	rel, err := update.CheckLatest(r.Context(), client, updateOwner, updateRepo)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "check-failed", err.Error(), "")
		return
	}
	available := update.UpdateAvailable(Version, rel.Version)
	if a.updateStore != nil {
		_, _ = a.updateStore.Log(r.Context(), update.Record{
			FromVersion: Version, ToVersion: rel.Version, Status: "checked",
			Detail: fmt.Sprintf("current=%s latest=%s available=%t (via VayuOS)", Version, rel.Version, available),
		})
	}
	enabled, hasKey := selfUpdateConfigured()
	modeOK := update.PreflightMode(string(mode.Global.Current())) == nil
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"current":   Version,
		"latest":    rel.Version,
		"available": available,
		"notes":     rel.Notes,
		"url":       rel.URL,
		"canApply":  modeOK,
		"signed":    hasKey,
		"enabled":   enabled,
		"hasKey":    hasKey,
		"mode":      string(mode.Global.Current()),
	})
}

// handleOSUpdateApply verifies and installs the latest release. With
// {"restart": true} it re-execs the process to activate the new binary; with
// {"dryRun": true} it verifies signatures without writing anything.
func (a *App) handleOSUpdateApply(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	// Backup defaults to ON when the field is absent, so older callers / the CLI
	// keep their safe behaviour; the VayuOS panel sends it explicitly from the
	// operator's checkbox. A pointer lets us tell "omitted" from "false".
	var body struct {
		DryRun  bool  `json:"dryRun"`
		Restart bool  `json:"restart"`
		Backup  *bool `json:"backup"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // empty body → zero-value defaults
	backup := body.Backup == nil || *body.Backup

	pubKey := os.Getenv("VAYU_RELEASE_PUBKEY")
	curMode := string(mode.Global.Current())
	// The caller is an authenticated admin who explicitly clicked Update — that
	// is the opt-in. We only refuse in modes that forbid mutating the binary.
	// Verification still happens in ApplyVerified: checksum always, plus Ed25519
	// signature when a release key is pinned (AllowUnsigned covers the no-key case).
	if err := update.PreflightMode(curMode); err != nil {
		writeAPIError(w, r, http.StatusPreconditionFailed, "preflight", err.Error(), "")
		return
	}

	binPath, err := os.Executable()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "exe-error", err.Error(), "")
		return
	}
	// Preflight: the binary's own directory must be writable, or the atomic swap
	// will fail. The most common cause on a hardened deployment is the systemd
	// sandbox (ProtectSystem=strict/full) making /usr/local/bin read-only — which
	// otherwise surfaces as a confusing mid-update failure. Fail fast with the fix.
	if why := binaryDirWritable(binPath); why != "" {
		writeAPIError(w, r, http.StatusPreconditionFailed, "binary-readonly",
			"Cannot install the update because the binary location is not writable: "+why+
				" Make "+filepath.Dir(binPath)+" writable by the service (e.g. add it to the systemd unit's ReadWritePaths=, or relax ProtectSystem=), then retry. Until then, update from the shell with scripts/update-vayupress.sh.", "")
		return
	}

	st := a.updateStore
	var histID int64
	if st != nil {
		histID, _ = st.Log(r.Context(), update.Record{FromVersion: Version, Status: "started", Detail: fmt.Sprintf("dry_run=%t (via VayuOS)", body.DryRun)})
	}

	// A generous timeout: release binaries can be large and links slow.
	client := &http.Client{Timeout: 15 * time.Minute, Transport: safeOutboundTransport()}
	opt := update.ApplyOptions{
		Current:       Version,
		DryRun:        body.DryRun,
		PubKeyHex:     pubKey,
		BinaryPath:    binPath,
		AllowUnsigned: true, // admin-initiated; checksum-verified when no key is pinned
	}
	// Pre-update database backup is the operator's choice. When enabled we point
	// ApplyVerified at the DB so it snapshots before swapping the binary; when
	// disabled we leave DBPath empty so it skips the backup entirely. A binary
	// update never rewrites the database, and the previous binary is always kept
	// as <binary>.bak for instant rollback, so skipping is safe — and it avoids a
	// slow/failing snapshot stalling the update on very large databases.
	if backup {
		opt.DBPath = config.Cfg.DBPath
		opt.BackupDir = config.Cfg.CacheDir + "/update-backups"
	}
	newVersion, err := update.ApplyVerified(r.Context(), client, updateOwner, updateRepo, opt, st)
	if err != nil {
		if st != nil && histID > 0 {
			_ = st.MarkComplete(r.Context(), histID, "failed", err.Error())
		}
		msg := err.Error()
		// Make the most common, recoverable failure self-explanatory: the
		// pre-update backup couldn't be written (slow/large DB, low space). The
		// binary was NOT touched, so the operator can simply retry without backup.
		if backup && strings.Contains(msg, "backup") {
			msg = "The pre-update database backup could not be completed, so the update was not applied (your binary is unchanged). " +
				"Untick “Back up the database first” and try again — a binary update never modifies your database, and the previous binary is kept for rollback — or take a backup from the Export section first. Original error: " + msg
		}
		writeAPIError(w, r, http.StatusBadGateway, "apply-failed", msg, "")
		return
	}

	if body.DryRun {
		if st != nil && histID > 0 {
			_ = st.MarkComplete(r.Context(), histID, "checked", "dry-run verification passed for "+newVersion)
		}
		writeJSON(w, r, http.StatusOK, map[string]interface{}{
			"status": "verified", "version": newVersion,
			"note": "Signature + checksum verified. Nothing was written (dry run).",
		})
		return
	}

	if st != nil && histID > 0 {
		_ = st.MarkComplete(r.Context(), histID, "success", "applied "+newVersion+" via VayuOS")
	}
	dbpkg.AuditLog("update.apply", dbpkg.AuditActor(r), newVersion, "binary updated "+Version+" -> "+newVersion+" via VayuOS")
	logging.LogInfo("update", "applied "+newVersion+" via VayuOS admin")

	if body.Restart {
		update.ScheduleRestartExec(binPath, 1500*time.Millisecond, restartCleanup)
		writeJSON(w, r, http.StatusOK, map[string]interface{}{
			"status": "updated-restarting", "version": newVersion,
			"note": "Update installed. The service is re-launching to activate v" + newVersion + ".",
		})
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"status": "updated", "version": newVersion,
		"note": update.RestartInstructions(newVersion),
	})
}

// handleOSUpdateRestart re-execs the running process. Used to activate an
// already-installed update or a staged database restore.
func (a *App) handleOSUpdateRestart(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	dbpkg.AuditLog("update.restart", dbpkg.AuditActor(r), "", "operator-initiated restart via VayuOS")
	update.ScheduleRestart(1200*time.Millisecond, restartCleanup)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"status": "restarting"})
}

// handleOSUpdateRollback swaps the previous binary (kept as <binary>.bak by a
// prior apply) back over the running binary, then restarts to activate it.
func (a *App) handleOSUpdateRollback(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	binPath, err := os.Executable()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "exe-error", err.Error(), "")
		return
	}
	bak := binPath + ".bak"
	if _, err := os.Stat(bak); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "no-rollback", "No rollback artifact found — nothing to roll back to.", "")
		return
	}

	st := a.updateStore
	var histID int64
	if st != nil {
		histID, _ = st.Log(r.Context(), update.Record{ToVersion: Version, Status: "started", Detail: "rollback (via VayuOS)"})
	}
	if err := os.Rename(bak, binPath); err != nil {
		if st != nil && histID > 0 {
			_ = st.MarkComplete(r.Context(), histID, "failed", "rollback: "+err.Error())
		}
		writeAPIError(w, r, http.StatusInternalServerError, "rollback-failed", err.Error(), "")
		return
	}
	if st != nil && histID > 0 {
		_ = st.MarkComplete(r.Context(), histID, "rolled_back", "rolled back from "+Version)
	}
	dbpkg.AuditLog("update.rollback", dbpkg.AuditActor(r), "", "rolled back binary via VayuOS")
	update.ScheduleRestartExec(binPath, 1200*time.Millisecond, restartCleanup)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"status": "rolled-back-restarting"})
}

// handleOSUpdateHistory returns recent update_history rows as JSON.
func (a *App) handleOSUpdateHistory(w http.ResponseWriter, r *http.Request) {
	if a.updateStore == nil {
		writeJSON(w, r, http.StatusOK, map[string]interface{}{"history": []any{}})
		return
	}
	recs, err := a.updateStore.List(r.Context(), 50)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "history-error", err.Error(), "")
		return
	}
	out := make([]map[string]interface{}, 0, len(recs))
	for _, rec := range recs {
		out = append(out, map[string]interface{}{
			"id":        rec.ID,
			"from":      rec.FromVersion,
			"to":        rec.ToVersion,
			"status":    rec.Status,
			"detail":    rec.Detail,
			"startedAt": rec.StartedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"history": out})
}

// ── Backup / export / import ───────────────────────────────────────────────

// snapshotTmpDir returns a directory the service can actually write large
// temporary backup files into, trying the configured TMP_DIR first, then the OS
// temp dir, then the database's own directory (guaranteed writable, since the DB
// lives there). This prevents export failures when TMP_DIR is unset or not
// writable under a hardened service sandbox.
func snapshotTmpDir() string {
	for _, d := range []string{config.Cfg.TmpDir, os.TempDir(), filepath.Dir(config.Cfg.DBPath)} {
		if strings.TrimSpace(d) == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			continue
		}
		probe, err := os.CreateTemp(d, ".vp-probe-*")
		if err != nil {
			continue
		}
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
		return d
	}
	return os.TempDir()
}

// handleOSBackupExport builds a full snapshot (.tar.gz) and serves it as a
// download. The archive is built to a temp file FIRST so that any failure
// returns a clean JSON error instead of a truncated 0-byte download; it is then
// served with http.ServeContent, which sets a real Content-Length (so the
// browser shows accurate progress) and streams from disk in constant memory
// regardless of size. The write deadline is lifted for large transfers.
func (a *App) handleOSBackupExport(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}

	tmpDir := snapshotTmpDir()
	archive, err := os.CreateTemp(tmpDir, "vp-backup-*.tar.gz")
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "tmp-error",
			"Could not create a temporary file for the backup: "+err.Error(), "")
		return
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)

	// Build the whole archive before sending any response header.
	exportErr := update.ExportSnapshot(r.Context(), archive, dbpkg.DB, config.Cfg.DBPath, tmpDir, Version)
	closeErr := archive.Close()
	if exportErr != nil {
		logging.LogError("update", "snapshot export failed", exportErr.Error())
		writeAPIError(w, r, http.StatusInternalServerError, "export-failed", "Backup failed: "+exportErr.Error(), "")
		return
	}
	if closeErr != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "export-failed", "Backup failed while flushing: "+closeErr.Error(), "")
		return
	}

	f, err := os.Open(archivePath)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "export-failed", "Backup file unreadable: "+err.Error(), "")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "export-failed", err.Error(), "")
		return
	}
	if fi.Size() == 0 {
		writeAPIError(w, r, http.StatusInternalServerError, "export-empty", "Backup produced an empty archive.", "")
		return
	}

	filename := fmt.Sprintf("vayupress-backup-v%s-%s.tar.gz", Version, time.Now().UTC().Format("20060102T150405Z"))
	// Lift the server WriteTimeout for a potentially large download.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")

	dbpkg.AuditLog("backup.export", dbpkg.AuditActor(r), filename,
		fmt.Sprintf("full snapshot exported via VayuOS (%d bytes)", fi.Size()))

	// ServeContent sets Content-Length, supports range/resume, and streams the
	// file from disk — constant memory, no size limit.
	http.ServeContent(w, r, filename, fi.ModTime(), f)
}

// handleOSBackupImport accepts a multipart upload of a snapshot, validates it,
// stages it for restore, and restarts the service to apply it. The read
// deadline is lifted and the upload is consumed via a streaming MultipartReader
// (never ParseMultipartForm), so there is no size limit and no buffering.
func (a *App) handleOSBackupImport(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	// Lift the server ReadTimeout for a potentially large upload.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetReadDeadline(time.Time{})
	}

	mr, err := r.MultipartReader()
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-multipart", "Expected a multipart file upload.", "")
		return
	}

	var manifest *update.SnapshotManifest
	found := false
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		if part.FormName() != "snapshot" {
			_ = part.Close()
			continue
		}
		found = true
		// Pass an empty tmpDir so StageRestore extracts into the database's own
		// directory — same filesystem as the pending-restore target, so the final
		// swap is an atomic rename rather than a cross-device copy.
		manifest, err = update.StageRestore(r.Context(), part, config.Cfg.DBPath, "")
		_ = part.Close()
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "restore-invalid", err.Error(), "")
			return
		}
		break
	}
	if !found {
		writeAPIError(w, r, http.StatusBadRequest, "no-file", `No "snapshot" file field in the upload.`, "")
		return
	}

	detail := "snapshot staged for restore via VayuOS"
	if manifest != nil {
		detail = fmt.Sprintf("staged restore: app=%s created=%s settings=%d via VayuOS",
			manifest.AppVersion, manifest.CreatedAt.Format(time.RFC3339), manifest.SettingsCount)
	}
	dbpkg.AuditLog("backup.import", dbpkg.AuditActor(r), config.Cfg.DBPath, detail)
	logging.LogInfo("update", detail)

	// The staged DB is swapped in by ApplyPendingRestore at next startup; restart
	// now to complete the restore atomically.
	update.ScheduleRestart(1500*time.Millisecond, restartCleanup)
	resp := map[string]interface{}{
		"status": "restoring-restarting",
		"note":   "Backup validated and staged. The service is restarting to load the restored data.",
	}
	if manifest != nil {
		resp["createdAt"] = manifest.CreatedAt.UTC().Format(time.RFC3339)
		resp["appVersion"] = manifest.AppVersion
		resp["settingsCount"] = manifest.SettingsCount
	}
	writeJSON(w, r, http.StatusOK, resp)
}
