// SPDX-License-Identifier: Apache-2.0

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
// Two listed files are not removed either: the last restore point (the rule
// Backups uses, deleteRestorePoint) and a log this server is writing to.

import (
	"encoding/json"
	"errors"
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
	"github.com/johalputt/vayupress/internal/ui"
)

// storageLogDir / storageBackupDir let an operator point the panel at custom
// locations; the defaults match the shipped deploy scripts.
func storageLogDir() string    { return config.EnvOr("VAYU_LOG_DIR", "/var/log/vayupress") }
func storageBackupDir() string { return config.EnvOr("VAYU_BACKUP_DIR", "/var/backups/vayupress") }

// updateBackupDir is where the self-update engine writes pre-update DB backups.
func updateBackupDir() string { return filepath.Join(config.Cfg.CacheDir, "update-backups") }

// managedFile is one operator-manageable artefact. Keep, when set, is why it
// cannot be deleted, as the table shows it in place of Delete.
type managedFile struct {
	Path     string
	Name     string
	Category string
	Size     int64
	ModTime  time.Time
	Keep     string
}

// What a managed file is, as the Type column names it. A database copy and a
// restore point were both "Database backup", though one is the whole database
// in the clear, left by an update, and the other is Backups' encrypted,
// tested archive: an operator clearing space needs to tell them apart.
const (
	fileRestorePoint = "Restore point"
	fileDatabaseCopy = "Database copy"
	fileLog          = "Log"
	fileTemp         = "Temp file"
)

const writingLogRefusal = "This server writes its output to it: deleting it frees no space until a restart, and loses what is written after."

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
func (a *App) managedStorageFiles() []managedFile {
	live := liveDBPaths()
	points := a.restorePointPaths()
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
	add(fileDatabaseCopy, updateBackupDir(), nil)
	add(fileDatabaseCopy, filepath.Dir(config.Cfg.DBPath), func(name string) bool {
		return strings.Contains(name, ".backup-") || strings.HasSuffix(name, ".bak") || strings.HasSuffix(name, ".db.old")
	})
	add(fileDatabaseCopy, storageBackupDir(), nil)

	// Logs.
	add(fileLog, storageLogDir(), func(name string) bool {
		return strings.Contains(name, ".log")
	})

	// Temporary files (export staging, probes, leftover restore artefacts).
	add(fileTemp, config.Cfg.TmpDir, nil)

	for i := range out {
		switch f := &out[i]; {
		case points[f.Path]:
			f.Category = fileRestorePoint
			if len(points) == 1 {
				f.Keep = "Your only restore point"
			}
		case f.Category == fileLog && isServerOutput(f.Path):
			f.Keep = "Being written"
		}
	}

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

// restorePointPaths is Backups' own listing of its restore points, so Storage
// names one only where Backups would.
func (a *App) restorePointPaths() map[string]bool {
	points := map[string]bool{}
	if a.vayuKeep == nil {
		return points
	}
	gens, _ := a.vayuKeep.List()
	for _, g := range gens {
		points[filepath.Clean(g.Path)] = true
	}
	return points
}

// isServerOutput reports whether this process writes its standard output or
// error to path. A unit from before 5 July 2026 appended both to log files in
// the log directory (StandardOutput=append:); one written since logs to the
// journal, and then no log file is open. The open descriptor is asked rather
// than the file's name, which says nothing about which unit an install runs.
func isServerOutput(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	for _, out := range []*os.File{os.Stdout, os.Stderr} {
		if oi, err := out.Stat(); err == nil && os.SameFile(fi, oi) {
			return true
		}
	}
	return false
}

// managedFileByPath returns the managed file at the cleaned path, or ok=false if
// it is not currently a managed artefact (the authorisation check for both
// download and delete).
func (a *App) managedFileByPath(p string) (managedFile, bool) {
	clean := filepath.Clean(p)
	for _, f := range a.managedStorageFiles() {
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
	files := a.managedStorageFiles()
	var totalManaged int64
	for _, f := range files {
		totalManaged += f.Size
	}
	tone, state := storageState(st)

	body := ui.Status(ui.StatusPage{
		Title:  "Storage",
		Tone:   tone,
		State:  state,
		Detail: ui.Text("VayuPress holds " + humanBytes(st.DBSize+st.CacheSize+st.MediaSize+st.BackupsSize) + " of it · live readings"),
	},
		ui.Section("Memory and disk", "", ui.Rows(
			ui.Row{Label: "Memory", Hint: "VayuPress uses " + humanBytes(int64(st.ProcRSS)) + ", " + humanBytes(int64(st.GoHeapInUse)) +
				" of it Go heap, in " + strconv.Itoa(st.Goroutines) + " goroutines",
				Control: ui.State(pctTone(st.memPct()), humanBytes(int64(st.MemUsed))+" of "+humanBytes(int64(st.MemTotal))+" used")},
			ui.Row{Label: "Disk", Hint: "The filesystem at " + st.DiskPath,
				Control: ui.State(pctTone(st.diskPct()), humanBytes(int64(st.DiskFree))+" free of "+humanBytes(int64(st.DiskTotal)))},
		)),
		ui.Section("What VayuPress holds", "", ui.Table([]string{"Component", "Path", "Size"}, [][]ui.HTML{
			{"Database, with its journal", `<code>` + ui.Text(config.Cfg.DBPath) + `</code>`, ui.Text(humanBytes(st.DBSize))},
			{"Rendered pages", `<code>` + ui.Text(config.Cfg.CacheDir) + `</code>`, ui.Text(humanBytes(st.CacheSize))},
			{"Media library", `<code>` + ui.Text(config.Cfg.MediaDir) + `</code>`, ui.Text(humanBytes(st.MediaSize))},
			{"Pre-update backups", `<code>` + ui.Text(updateBackupDir()) + `</code>`, ui.Text(humanBytes(st.BackupsSize))},
		}, "")),
		cacheSection(st.CacheSize),
		ui.Section("Backups, logs and temporary files", humanBytes(totalManaged)+" in "+strconv.Itoa(len(files))+" file"+plural(len(files)),
			ui.Join(`<p class="page-sub">`+ui.Brief("Download one to keep it off the server, or delete it to reclaim the space. "+
				"A database copy is the whole database, unencrypted, left by an update; a restore point is an encrypted backup from Backups. "+
				"The live database and its journal are never listed, and the only restore point cannot be deleted.")+`</p>`,
				ui.HTML(storageFilesTable(files)),
				`<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>`)),
	)

	writeOSHTML(w, r, adminOSLayout(nonce, "Storage", "storage", cfg, htmpl.HTML(string(body)+
		`<script nonce="`+nonce+`" src="/os/static/js/admin-os-storage.js?v=`+assetVer("js/admin-os-storage.js")+`"></script>`)))
}

// storageState is the sentence the Storage page opens on: how much room is
// left, and whether it is running out. Disk comes first because a full disk
// stops the site, where full memory slows it.
func storageState(st sysStats) (tone, state string) {
	switch d, m := st.diskPct(), st.memPct(); {
	case d >= 90:
		return "danger", "The disk is " + strconv.Itoa(d) + "% full: " + humanBytes(int64(st.DiskFree)) + " left"
	case m >= 90:
		return "warn", "Memory is " + strconv.Itoa(m) + "% used"
	case d >= 75:
		return "warn", humanBytes(int64(st.DiskFree)) + " left on the disk, " + strconv.Itoa(100-d) + "% of it"
	}
	return "ok", humanBytes(int64(st.DiskFree)) + " free on the disk, and memory to spare"
}

// pctTone is a usage percentage as a state's tone: the same 75 and 90 the
// sentence uses.
func pctTone(pct int) string {
	switch {
	case pct >= 90:
		return "danger"
	case pct >= 75:
		return "warn"
	}
	return "ok"
}

// cacheSection is Refresh every page: what the rendered pages and stale temp
// files hold, the Cloudflare purge when there is a Cloudflare to purge, and
// the refresh's progress. pageBytes comes from the background footprint, never
// a walk on this request.
func cacheSection(pageBytes int64) ui.HTML {
	tempFiles, tempBytes := staleTemp(config.Cfg.TmpDir, staleTempAge, time.Now(), false)
	rows := []ui.Row{
		{Label: "Rendered pages", Hint: "Home, post and tag pages, for every domain", Control: ui.Text(humanBytes(pageBytes))},
		{Label: "Temporary files", Hint: "Untouched for an hour or more", Control: ui.Text(humanBytes(tempBytes) + " · " + itoaSafe(tempFiles) + " file" + plural(tempFiles))},
	}
	if cloudflareConfigured() {
		rows = append(rows, ui.Row{Label: "Cloudflare", Hint: "Purge Cloudflare's copy of every page as well",
			Control: `<input type="checkbox" data-cache-cdn checked aria-label="Also purge Cloudflare's copy of every page">`})
	}
	rows = append(rows, ui.Row{Label: "Refresh every page",
		Hint:    "Every page is rebuilt in the background, as fast as the server can spare. Visitors keep the current copy until its new one is ready.",
		Control: `<button type="button" class="btn btn--primary btn--sm" data-pages-refresh>Refresh every page</button>`})
	return ui.Section("Pages", "rebuilt from the database on their own", ui.Join(
		ui.Rows(rows...),
		ui.HTML(refreshStatusHTML(warmProgress())),
		`<div data-cache-msg role="status" aria-live="polite" class="action-msg"></div>`,
	))
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
		pick := `<input type="checkbox" data-file-select value="` + enc + `" aria-label="Select ` + html.EscapeString(f.Name) + `">`
		del := `<button type="button" class="btn btn--danger btn--sm" data-file-delete data-path="` + enc + `" data-name="` + html.EscapeString(f.Name) + `">Delete</button>`
		if f.Keep != "" {
			pick, del = "", `<span class="muted text-sm">`+html.EscapeString(f.Keep)+`</span>`
		}
		rows.WriteString(`<tr data-file-row>
  <td>` + pick + `</td>
  <td class="row-title">` + html.EscapeString(f.Name) + `</td>
  <td><span class="chip">` + html.EscapeString(f.Category) + `</span></td>
  <td class="muted text-sm">` + humanBytes(f.Size) + `</td>
  <td class="muted text-sm">` + config.FormatSite(f.ModTime, "2 Jan 2006 15:04") + `</td>
  <td class="row-actions">
    <a class="btn btn--ghost btn--sm" href="/os/api/storage/download?path=` + qparam(f.Path) + `" download>Download</a>
    ` + del + `
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

// ── Download ─────────────────────────────────────────────────────────────────

func (a *App) handleOSStorageDownload(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	f, ok := a.managedFileByPath(r.URL.Query().Get("path"))
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

	freed := int64(0)
	removed := []string{}
	var failed []string
	for _, p := range paths {
		f, ok := a.managedFileByPath(p)
		if !ok {
			failed = append(failed, filepath.Base(p))
			continue
		}
		var err error
		switch {
		case f.Category == fileRestorePoint:
			_, err = a.deleteRestorePoint(f.Name)
			if errors.Is(err, errOnlyRestorePoint) {
				failed = append(failed, f.Name+": "+onlyRestorePointRefusal)
				continue
			}
		case f.Keep != "":
			failed = append(failed, f.Name+": "+writingLogRefusal)
			continue
		default:
			err = os.Remove(f.Path)
		}
		if err != nil {
			logging.LogError("storage", "delete managed file "+f.Path, err.Error())
			failed = append(failed, f.Name)
			continue
		}
		removed = append(removed, f.Path)
		freed += f.Size
		dbpkg.AuditLog("storage.delete", dbpkg.AuditActor(r), f.Name, "deleted managed file via VayuOS")
	}

	resp := map[string]interface{}{
		"deleted": len(removed),
		"removed": removed,
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
