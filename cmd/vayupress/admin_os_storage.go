package main

// admin_os_storage.go — VayuOS "Storage & System" panel (administrators only).
//
// Brings two operator needs into the admin UI without shell access:
//
//  1. Resource usage. How much RAM the VayuPress process and the host are
//     using, and how much disk (NVMe) the filesystem holding the database has
//     left — plus the on-disk footprint of the database, cache, media and
//     backups.
//
//  2. Managed files. The artefacts VayuPress creates over time — database
//     backups (including the automatic pre-update snapshots), log files and
//     temporary files — listed with their size and age, each downloadable and
//     deletable in one click so an operator can reclaim space safely.
//
// Security posture: every endpoint here is admin-role gated and the writes are
// CSRF-protected. Download and delete never trust a client-supplied path: the
// requested path must exactly match a file currently enumerated by
// managedStorageFiles() (re-scanned on each call), so path traversal is
// impossible and the live database / WAL can never be served or removed.

import (
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
)

// storageLogDir / storageBackupDir let an operator point the panel at custom
// locations; the defaults match the shipped deploy scripts.
func storageLogDir() string    { return config.EnvOr("VAYU_LOG_DIR", "/var/log/vayupress") }
func storageBackupDir() string { return config.EnvOr("VAYU_BACKUP_DIR", "/var/backups/vayupress") }

// updateBackupDir is where the self-update engine writes pre-update DB backups.
func updateBackupDir() string { return filepath.Join(config.Cfg.CacheDir, "update-backups") }

// managedFile is one operator-manageable artefact.
type managedFile struct {
	Path     string
	Name     string
	Category string
	Size     int64
	ModTime  time.Time
}

// liveDBPaths returns the paths that must NEVER be offered for download/delete.
func liveDBPaths() map[string]bool {
	db := filepath.Clean(config.Cfg.DBPath)
	return map[string]bool{
		db:              true,
		db + "-wal":     true,
		db + "-shm":     true,
		db + "-journal": true,
	}
}

// managedStorageFiles enumerates every file the panel manages, freshly scanned.
// Scans are one level deep (these locations are flat) and only ever return
// regular files; the live database and its WAL/SHM are always excluded.
func managedStorageFiles() []managedFile {
	live := liveDBPaths()
	var out []managedFile

	add := func(category, dir string, include func(name string) bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if include != nil && !include(name) {
				continue
			}
			full := filepath.Clean(filepath.Join(dir, name))
			if live[full] {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			out = append(out, managedFile{
				Path:     full,
				Name:     name,
				Category: category,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
			})
		}
	}

	// Database backups: the self-update pre-update snapshots, the script's
	// timestamped backups next to the DB, and any dedicated backup dir.
	add("Database backup", updateBackupDir(), nil)
	add("Database backup", filepath.Dir(config.Cfg.DBPath), func(name string) bool {
		return strings.Contains(name, ".backup-") || strings.HasSuffix(name, ".bak") || strings.HasSuffix(name, ".db.old")
	})
	add("Database backup", storageBackupDir(), nil)

	// Logs.
	add("Log", storageLogDir(), func(name string) bool {
		return strings.Contains(name, ".log")
	})

	// Temporary files (export staging, probes, leftover restore artefacts).
	add("Temp file", config.Cfg.TmpDir, nil)

	// De-duplicate (the DB dir and a custom backup dir could overlap) and sort
	// newest-first within a stable category order.
	seen := map[string]bool{}
	deduped := out[:0]
	for _, f := range out {
		if seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		deduped = append(deduped, f)
	}
	sort.SliceStable(deduped, func(i, j int) bool {
		if deduped[i].Category != deduped[j].Category {
			return deduped[i].Category < deduped[j].Category
		}
		return deduped[i].ModTime.After(deduped[j].ModTime)
	})
	return deduped
}

// managedFileByPath returns the managed file at the cleaned path, or ok=false if
// it is not currently a managed artefact (the authorisation check for both
// download and delete).
func managedFileByPath(p string) (managedFile, bool) {
	clean := filepath.Clean(p)
	for _, f := range managedStorageFiles() {
		if f.Path == clean {
			return f, true
		}
	}
	return managedFile{}, false
}

// ── Page ─────────────────────────────────────────────────────────────────────

func (a *App) handleOSStorage(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	st := collectSysStats(config.Cfg.DBPath, config.Cfg.CacheDir, config.Cfg.MediaDir, updateBackupDir())
	files := managedStorageFiles()

	memBarCls := pctBarClass(st.memPct())
	diskBarCls := pctBarClass(st.diskPct())

	var totalManaged int64
	for _, f := range files {
		totalManaged += f.Size
	}

	body := `<div class="page-header">
  <h1>Storage &amp; System</h1>
  <div class="page-actions"><span class="text-sm muted">Administrator only · live readings</span></div>
</div>

<div class="grid grid-2 mb-6">
  <div class="card">
    <div class="card-title">Memory (RAM)</div>
    <div class="progress"><div class="` + memBarCls + ` ` + storagePctWidth(st.memPct()) + `"></div></div>
    <div class="flex justify-between mt-3">
      <span class="text-xs muted">System: ` + humanBytes(int64(st.MemUsed)) + ` / ` + humanBytes(int64(st.MemTotal)) + ` used (` + strconv.Itoa(st.memPct()) + `%)</span>
      <span class="text-xs muted">VayuPress process: ` + humanBytes(int64(st.ProcRSS)) + `</span>
    </div>
    <div class="text-xs muted mt-2">Go heap in use ` + humanBytes(int64(st.GoHeapInUse)) + ` · ` + strconv.Itoa(st.Goroutines) + ` goroutines</div>
  </div>
  <div class="card">
    <div class="card-title">Disk (NVMe)</div>
    <div class="progress"><div class="` + diskBarCls + ` ` + storagePctWidth(st.diskPct()) + `"></div></div>
    <div class="flex justify-between mt-3">
      <span class="text-xs muted">` + humanBytes(int64(st.DiskUsed)) + ` / ` + humanBytes(int64(st.DiskTotal)) + ` used (` + strconv.Itoa(st.diskPct()) + `%)</span>
      <span class="text-xs muted">` + humanBytes(int64(st.DiskFree)) + ` free</span>
    </div>
    <div class="text-xs muted mt-2">Filesystem at <code>` + html.EscapeString(st.DiskPath) + `</code></div>
  </div>
</div>

<div class="card mb-6">
  <div class="card-title">VayuPress footprint</div>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Component</th><th>Path</th><th>Size</th></tr></thead>
    <tbody>
      <tr><td>Database (+ WAL/SHM)</td><td class="mono text-sm">` + html.EscapeString(config.Cfg.DBPath) + `</td><td>` + humanBytes(st.DBSize) + `</td></tr>
      <tr><td>Render cache</td><td class="mono text-sm">` + html.EscapeString(config.Cfg.CacheDir) + `</td><td>` + humanBytes(st.CacheSize) + `</td></tr>
      <tr><td>Media library</td><td class="mono text-sm">` + html.EscapeString(config.Cfg.MediaDir) + `</td><td>` + humanBytes(st.MediaSize) + `</td></tr>
      <tr><td>Pre-update backups</td><td class="mono text-sm">` + html.EscapeString(updateBackupDir()) + `</td><td>` + humanBytes(st.BackupsSize) + `</td></tr>
    </tbody>
  </table></div>
</div>

<div class="card" data-storage-card>
  <div class="card-title">Managed files <span class="count-pill">` + strconv.Itoa(len(files)) + `</span></div>
  <p class="text-sm muted mb-4">Backups, logs and temporary files VayuPress has created. Download one to keep it off-server, or delete it to reclaim space — total here is <strong>` + humanBytes(totalManaged) + `</strong>. The live database and its WAL are never listed and can never be deleted from here.</p>
  ` + storageFilesTable(files) + `
  <div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
</div>

<script nonce="` + nonce + `" src="/os/static/js/admin-os-storage.js?v=` + assetVer("js/admin-os-storage.js") + `"></script>`

	writeOSHTML(w, adminOSLayout(nonce, "Storage & System", "storage", cfg, htmpl.HTML(body)))
}

// storageFilesTable renders the managed-files table with per-row download +
// delete and a bulk "delete selected" bar. Empty state when there is nothing.
func storageFilesTable(files []managedFile) string {
	if len(files) == 0 {
		return `<div class="table-empty">No backups, logs or temporary files right now — nothing to clean up.</div>`
	}
	var rows strings.Builder
	for _, f := range files {
		enc := html.EscapeString(f.Path)
		rows.WriteString(`<tr data-file-row>
  <td><input type="checkbox" data-file-select value="` + enc + `" aria-label="Select ` + html.EscapeString(f.Name) + `"></td>
  <td class="row-title">` + html.EscapeString(f.Name) + `</td>
  <td><span class="chip">` + html.EscapeString(f.Category) + `</span></td>
  <td class="muted text-sm">` + humanBytes(f.Size) + `</td>
  <td class="muted text-sm">` + f.ModTime.UTC().Format("2 Jan 2006 15:04") + `</td>
  <td class="row-actions">
    <a class="btn btn--ghost btn--sm" href="/os/api/storage/download?path=` + qparam(f.Path) + `" download>Download</a>
    <button type="button" class="btn btn--danger btn--sm" data-file-delete data-path="` + enc + `" data-name="` + html.EscapeString(f.Name) + `">Delete</button>
  </td>
</tr>`)
	}
	return `<div class="bulk-bar" data-file-bulkbar hidden>
    <span class="text-sm"><span data-file-bulk-count>0</span> selected</span>
    <button type="button" class="btn btn--danger btn--sm" data-file-bulk-delete>Delete selected</button>
  </div>
  <div class="table-wrap"><table class="table">
    <thead><tr><th><input type="checkbox" data-file-select-all aria-label="Select all files"></th><th>Name</th><th>Type</th><th>Size</th><th>Modified</th><th></th></tr></thead>
    <tbody>` + rows.String() + `</tbody>
  </table></div>`
}

// pctBarClass returns the progress-bar colour class for a 0–100 usage value.
func pctBarClass(pct int) string {
	switch {
	case pct >= 90:
		return "progress__bar progress__bar--danger"
	case pct >= 75:
		return "progress__bar progress__bar--warn"
	default:
		return "progress__bar progress__bar--ok"
	}
}

// storagePctWidth maps a percentage to the shared width utility class (CSP-safe;
// reuses the dashboard storage-bar width buckets via storageWidthClass).
func storagePctWidth(pct int) string { return storageWidthClass(pct) }

// ── Download ─────────────────────────────────────────────────────────────────

func (a *App) handleOSStorageDownload(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	f, ok := managedFileByPath(r.URL.Query().Get("path"))
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, "not-managed", "That file is not a downloadable VayuPress artefact.", "")
		return
	}
	fh, err := os.Open(f.Path) //nolint:gosec // path is validated against the managed-file set above
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "open-failed", err.Error(), "")
		return
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "stat-failed", err.Error(), "")
		return
	}
	// Lift the server write deadline for a potentially large backup download.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeFilename(f.Name)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	dbpkg.AuditLog("storage.download", dbpkg.AuditActor(r), f.Name, "downloaded managed file via VayuOS")
	http.ServeContent(w, r, f.Name, fi.ModTime(), fh)
}

// ── Delete ───────────────────────────────────────────────────────────────────

func (a *App) handleOSStorageDelete(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	var body struct {
		Paths []string `json:"paths"`
		Path  string   `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	paths := body.Paths
	if body.Path != "" {
		paths = append(paths, body.Path)
	}
	if len(paths) == 0 {
		writeAPIError(w, r, http.StatusBadRequest, "no-path", "No file specified.", "")
		return
	}

	deleted, freed := 0, int64(0)
	var failed []string
	for _, p := range paths {
		f, ok := managedFileByPath(p)
		if !ok {
			failed = append(failed, filepath.Base(p))
			continue
		}
		if err := os.Remove(f.Path); err != nil {
			logging.LogError("storage", "delete managed file "+f.Path, err.Error())
			failed = append(failed, f.Name)
			continue
		}
		deleted++
		freed += f.Size
		dbpkg.AuditLog("storage.delete", dbpkg.AuditActor(r), f.Name, "deleted managed file via VayuOS")
	}

	resp := map[string]interface{}{
		"deleted": deleted,
		"freed":   humanBytes(freed),
	}
	if len(failed) > 0 {
		resp["failed"] = failed
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// ── small helpers ────────────────────────────────────────────────────────────

// sanitizeFilename strips any path separators from a name used in a
// Content-Disposition header (defence in depth — names come from the filesystem).
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, `"`, "")
	name = strings.ReplaceAll(name, "\n", "")
	name = strings.ReplaceAll(name, "\r", "")
	if name == "" || name == "." || name == ".." {
		return "download"
	}
	return name
}
