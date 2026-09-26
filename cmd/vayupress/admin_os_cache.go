// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_cache.go — "Refresh every page" on System › Storage.
//
// It used to be "Clear caches", which deleted every rendered page. On a large
// site that turned each page's worst query into sustained load: with no file
// there was no stale copy to serve, so every request, crawlers included,
// rendered from the database at once, and johal.in's console answered 502 for
// hours (2026-09-26). Now nothing is deleted that is still in use:
//
//   - every rendered page is marked out of date (render.CachePurgeAll); a
//     visitor keeps getting the current copy while its new one is rendered;
//   - the warmer rebuilds every page in the background, in batches the pacer
//     sizes from what the host is doing (cachewarm.go, internal/pace);
//   - the space a clear used to free comes from what nobody can ask for any
//     more: cached pages whose post is gone, removed in the same paced pass,
//     and VayuPress's own temp files untouched for an hour.
//
// Optionally it also asks Cloudflare to drop its copies, when Cloudflare is
// configured and this is not a Tor world. Media, backups, logs and the database
// are never touched.

import (
	"encoding/json"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
)

// staleTempAge is how long a temp file must sit untouched before the clear
// treats it as abandoned. Exports and restores write continuously while they
// run, so an hour without a write means nobody is coming back for it.
const staleTempAge = time.Hour

// The names VayuPress gives the files it writes into TMP_DIR. The writers
// create them from these patterns and staleTemp deletes only what matches, so
// the two cannot drift apart.
const (
	exportTempPattern = "vp-backup-*.tar.gz"
	probeTempPattern  = ".vp-probe-*"
)

// ownTempFile reports whether name is a file VayuPress writes into TMP_DIR.
// TMP_DIR is an operator setting and can name a shared or a data directory;
// anything else in it is not ours to delete.
func ownTempFile(name string) bool {
	for _, p := range []string{exportTempPattern, probeTempPattern} {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

// staleTemp finds VayuPress's own regular files directly in dir whose last
// write is older than maxAge and, with remove set, deletes them. It returns the
// files and bytes found (or, removing, the ones actually deleted). Directories,
// symlinks and every file VayuPress did not name are left alone.
func staleTemp(dir string, maxAge time.Duration, now time.Time, remove bool) (files int, bytes int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !ownTempFile(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < maxAge {
			continue
		}
		if remove && os.Remove(filepath.Join(dir, e.Name())) != nil {
			continue
		}
		files++
		bytes += info.Size()
	}
	return files, bytes
}

// refreshResult is what Refresh every page reports at once; the pass itself
// reports on the Storage page as it goes (refreshStatusHTML).
type refreshResult struct {
	Joined     bool   `json:"joined"` // a pass was already running and became the refresh
	TempFiles  int    `json:"temp_files"`
	FreedBytes int64  `json:"freed_bytes"`
	Freed      string `json:"freed"`
	CDN        string `json:"cdn"`
}

// refreshEveryPage marks every rendered page stale and starts the paced
// rebuild. The console's button and the API's full purge both call it.
func (a *App) refreshEveryPage(purgeCDN bool, actor string) refreshResult {
	render.CachePurgeAll()
	temps, tempBytes := staleTemp(config.Cfg.TmpDir, staleTempAge, time.Now(), true)
	// The sitemap, feed and robots.txt are rebuilt now rather than on a timer,
	// so search engines never find them older than the pages they list.
	render.Regenerate(generateSitemap, generateRSS, generateRobots, func() {
		refreshFootprint(config.Cfg.CacheDir, config.Cfg.MediaDir, updateBackupDir())
	})
	joined := a.startRefresh()

	cdn := "off"
	if purgeCDN && cloudflareConfigured() {
		cdn = "purged"
		if err := a.cloudflarePurge(map[string]any{"purge_everything": true}); err != nil {
			cdn = "failed: " + err.Error()
		}
	}
	dbpkg.AuditLog("storage.refresh-pages", actor, "caches",
		"every page marked for refresh; "+itoaSafe(temps)+" temp files ("+humanBytes(tempBytes)+") removed; cdn "+cdn)
	return refreshResult{Joined: joined, TempFiles: temps, FreedBytes: tempBytes, Freed: humanBytes(tempBytes), CDN: cdn}
}

func (a *App) handleOSStorageRefreshPages(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	var body struct {
		PurgeCDN bool `json:"purge_cdn"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	writeJSON(w, r, http.StatusOK, a.refreshEveryPage(body.PurgeCDN, dbpkg.AuditActor(r)))
}

// handleOSStorageRefreshStatus is the pass's progress, polled by the Storage
// page while it runs.
func (a *App) handleOSStorageRefreshStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(refreshStatusHTML(warmProgress())))
}

// refreshStatusHTML shows the pass in progress, or the last refresh: how far
// it has got, what it has removed, and the pace it runs at and why.
func refreshStatusHTML(run *warmRun) string {
	const id = `id="pages-refresh" role="status" aria-live="polite"`
	if run == nil || (!run.Refresh && !run.Finished.IsZero()) {
		return `<div ` + id + `></div>`
	}
	removed := ""
	if run.Removed > 0 {
		removed = ` · removed ` + itoaSafe(run.Removed) + ` page` + plural(run.Removed) + ` whose post is gone (` + humanBytes(run.Freed) + `)`
	}
	counts := itoaSafe(run.Checked) + ` of ` + itoaSafe(run.Total) + ` posts checked · ` + itoaSafe(run.Rebuilt) + ` page` + plural(run.Rebuilt) + ` rebuilt` + removed
	if !run.Finished.IsZero() {
		state := "Last refresh finished " + run.Finished.UTC().Format("2006-01-02 15:04") + " UTC"
		if run.Cancelled {
			state = "Last refresh stopped when VayuPress restarted; pages not reached refresh as visitors ask for them"
		}
		return `<div ` + id + ` class="text-sm muted mt-3">` + html.EscapeString(state) + `: ` + counts + `.</div>`
	}
	pct := 0
	if run.Total > 0 {
		pct = run.Checked * 100 / run.Total
	}
	return `<div ` + id + ` class="mt-3" hx-get="/os/storage/refresh-status" hx-trigger="every 2s" hx-swap="outerHTML">
  <progress class="progress" max="100" value="` + strconv.Itoa(pct) + `" aria-label="Pages refreshed">` + strconv.Itoa(pct) + `%</progress>
  <p class="text-sm mt-2">Refreshing every page: ` + counts + `.</p>
  <p class="text-sm muted">` + html.EscapeString(run.Pace.Verdict.Level.String()) + `, batches of ` + itoaSafe(run.Pace.Batch) + `: ` + html.EscapeString(run.Pace.Verdict.Reason) + `.</p>
</div>`
}
