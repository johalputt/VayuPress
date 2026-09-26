// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_cache.go — "Clear caches" on System › Storage: one control that
// frees the space held by data VayuPress can rebuild, and says how much.
//
// What it removes, and only this:
//   - rendered pages (render.CacheClear's allow-list; the pre-update backups,
//     search index and VayuShield lists beside them in CACHE_DIR stay);
//   - VayuPress's own files in TMP_DIR (export archives, write probes)
//     untouched for an hour: an export still being written is younger than
//     that, and nothing else in TMP_DIR is ours, whatever directory it names.
//
// Optionally it also asks Cloudflare to drop its copies, when Cloudflare is
// configured and this is not a Tor world. Media, backups, logs and the database
// are never touched: none of them can be rebuilt.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
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

// clearCacheRunning is held by the one cache clear in progress. A second click
// while it runs is told so rather than starting another: each clear unlinks
// every rendered page, and on a large site two at once meant twice the disk
// work at the moment every page starts being rebuilt from the database.
var clearCacheRunning sync.Mutex

func (a *App) handleOSStorageClearCache(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if !clearCacheRunning.TryLock() {
		writeAPIError(w, r, http.StatusConflict, "clear-running", "A cache clear is already running. It will finish on its own.", "")
		return
	}
	defer clearCacheRunning.Unlock()
	var body struct {
		PurgeCDN bool `json:"purge_cdn"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Tens of thousands of small files can take a few seconds to unlink.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Now().Add(2 * time.Minute))
	}
	pages, pageBytes := render.CacheClear()
	temps, tempBytes := staleTemp(config.Cfg.TmpDir, staleTempAge, time.Now(), true)
	// The sitemap, feed and robots.txt are rebuilt now rather than on a timer,
	// so search engines never find them older than the pages they list.
	go generateSitemap()
	go generateRSS()
	go generateRobots()
	go refreshFootprint(config.Cfg.CacheDir, config.Cfg.MediaDir, updateBackupDir())

	cdn := "off"
	if body.PurgeCDN && cloudflareConfigured() {
		cdn = "purged"
		if err := a.cloudflarePurge(map[string]any{"purge_everything": true}); err != nil {
			cdn = "failed: " + err.Error()
		}
	}
	freed := pageBytes + tempBytes
	dbpkg.AuditLog("storage.clear-cache", dbpkg.AuditActor(r), "caches",
		"cleared "+humanBytes(freed)+" ("+itoaSafe(pages)+" pages, "+itoaSafe(temps)+" temp files); cdn "+cdn)
	writeJSON(w, r, http.StatusOK, map[string]any{
		"pages":       pages,
		"temp_files":  temps,
		"freed_bytes": freed,
		"freed":       humanBytes(freed),
		"cdn":         cdn,
	})
}
