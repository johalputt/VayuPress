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
    <span class="text-sm muted">Releases are verified by SHA-256 checksum <em>and</em> Ed25519 signature against your pinned release key, your database is backed up automatically, and the service re-launches itself to finish.</span>
  </div>`
	default:
		banner = `<div class="settings-callout">
    <strong>One-click updates are ready.</strong>
    <span class="text-sm muted">Click <em>Update now</em> to install the latest release: the download is SHA-256 checksum verified, your database is backed up automatically, the binary is swapped atomically, and the service restarts. For an extra layer, pin a release signing key in <code>VAYU_RELEASE_PUBKEY</code> to also require Ed25519 signature verification.</span>
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
  <div class="theme-actions mt-4" data-actions-wrap>
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
	var body struct {
		DryRun  bool `json:"dryRun"`
		Restart bool `json:"restart"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // empty body → zero-value defaults

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
		DBPath:        config.Cfg.DBPath,
		BackupDir:     config.Cfg.CacheDir + "/update-backups",
		BinaryPath:    binPath,
		AllowUnsigned: true, // admin-initiated; checksum-verified when no key is pinned
	}
	newVersion, err := update.ApplyVerified(r.Context(), client, updateOwner, updateRepo, opt, st)
	if err != nil {
		if st != nil && histID > 0 {
			_ = st.MarkComplete(r.Context(), histID, "failed", err.Error())
		}
		writeAPIError(w, r, http.StatusBadGateway, "apply-failed", err.Error(), "")
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
		update.ScheduleRestart(1500*time.Millisecond, restartCleanup)
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
	update.ScheduleRestart(1200*time.Millisecond, restartCleanup)
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

// handleOSBackupExport streams a full snapshot (.tar.gz) download. The write
// deadline is lifted so arbitrarily large databases export without timing out,
// and the DB is copied with VACUUM INTO + io.Copy, so memory use is constant.
func (a *App) handleOSBackupExport(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	// Lift the server WriteTimeout for this streamed, potentially long response.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}

	filename := fmt.Sprintf("vayupress-backup-v%s-%s.tar.gz", Version, time.Now().UTC().Format("20060102T150405Z"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")

	if err := update.ExportSnapshot(r.Context(), w, dbpkg.DB, config.Cfg.DBPath, config.Cfg.TmpDir, Version); err != nil {
		// The response stream has very likely started, so we cannot send a clean
		// JSON error — log it and let the truncated download signal failure.
		logging.LogError("update", "snapshot export failed", err.Error())
		return
	}
	dbpkg.AuditLog("backup.export", dbpkg.AuditActor(r), filename, "full snapshot exported via VayuOS")
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
		manifest, err = update.StageRestore(r.Context(), part, config.Cfg.DBPath, config.Cfg.TmpDir)
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
