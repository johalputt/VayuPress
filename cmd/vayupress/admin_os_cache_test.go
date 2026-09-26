// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/pace"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
	"golang.org/x/sys/unix"
)

// Only a file untouched for the full age is abandoned. One seed per rule: a
// fresh file (an export still being written), a directory and a symlink each
// survive on their own.
func TestStaleTempRemovesOnlyAbandonedFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	write := func(name string, n int, mod time.Time) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	write("vp-backup-1.tar.gz", 300, old)
	write("vp-backup-2.tar.gz", 500, now.Add(-10*time.Minute))
	if err := os.Mkdir(filepath.Join(dir, "staging"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "staging"), old, old); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere.db")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "vp-backup-3.tar.gz")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	// The link itself is old too, so only the symlink rule can keep it: a young
	// link would survive on the age rule alone and prove nothing about this one.
	oldTV := []unix.Timeval{unix.NsecToTimeval(old.UnixNano()), unix.NsecToTimeval(old.UnixNano())}
	if err := unix.Lutimes(filepath.Join(dir, "vp-backup-3.tar.gz"), oldTV); err != nil {
		t.Fatal(err)
	}

	if n, b := staleTemp(dir, time.Hour, now, false); n != 1 || b != 300 {
		t.Fatalf("found %d files, %d bytes; want only vp-backup-1.tar.gz (1, 300)", n, b)
	}
	if n, b := staleTemp(dir, time.Hour, now, true); n != 1 || b != 300 {
		t.Errorf("removed %d files, %d bytes; want 1, 300", n, b)
	}
	for _, keep := range []string{"vp-backup-2.tar.gz", "staging", "vp-backup-3.tar.gz"} {
		if _, err := os.Lstat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s must survive the clear: %v", keep, err)
		}
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("the clear deleted a symlink's target: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vp-backup-1.tar.gz")); !os.IsNotExist(err) {
		t.Errorf("vp-backup-1.tar.gz survived the clear")
	}
}

// TMP_DIR is an operator setting. Point it at the data directory, or at a
// shared /tmp, and a clear that deletes "every old file in TMP_DIR" deletes a
// database that has not been written for an hour, or another program's files —
// while the Storage page promises the database is never touched. Only the
// files VayuPress itself writes there are candidates.
func TestClearingCachesDeletesNothingVayuPressDidNotWrite(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)
	for _, name := range []string{"vayupress.db", "vayupress.db-wal", "photo.jpg", "backup.tar.gz", ".vp-probe-7", "vp-backup-9.tar.gz"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	staleTemp(dir, time.Hour, time.Now(), true)
	for _, keep := range []string{"vayupress.db", "vayupress.db-wal", "photo.jpg", "backup.tar.gz"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("clearing caches deleted %s, which VayuPress did not write to TMP_DIR", keep)
		}
	}
	for _, gone := range []string{".vp-probe-7", "vp-backup-9.tar.gz"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("an abandoned %s survived the clear", gone)
		}
	}
}

// A Tor world never calls Cloudflare, even with credentials configured.
func TestCloudflarePurgeNeverLeavesATorWorld(t *testing.T) {
	prev := config.Cfg
	t.Cleanup(func() { config.Cfg = prev })
	config.Cfg.CFZoneID, config.Cfg.CFAPIToken = "zone", "token"
	config.Cfg.OnionMode = false
	if !cloudflareConfigured() {
		t.Fatal("clearnet with credentials must be able to purge")
	}
	config.Cfg.OnionMode = true
	if cloudflareConfigured() {
		t.Error("a Tor world must never send a Cloudflare purge")
	}
	config.Cfg.OnionMode = false
	config.Cfg.CFAPIToken = ""
	if cloudflareConfigured() {
		t.Error("without a token there is nothing to purge with")
	}
}

// Only an administrator refreshes every page.
func TestRefreshEveryPageRefusesANonAdmin(t *testing.T) {
	a := &App{}
	r := httptest.NewRequest(http.MethodPost, "/os/api/storage/refresh-pages", strings.NewReader(`{}`))
	r = r.WithContext(context.WithValue(r.Context(), ctxUserKey, &users.User{Role: users.RoleEditor}))
	w := httptest.NewRecorder()
	a.handleOSStorageRefreshPages(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("an editor got %d; want 403", w.Code)
	}
}

// pacedBy makes the warm pass ask a pacer that answers level, and stops any
// pass still running when the test ends, so none outlives its test.
func pacedBy(t *testing.T, level pace.Level) {
	t.Helper()
	ms := map[pace.Level]int64{pace.Go: 10, pace.Ease: 900, pace.Wait: 5000}[level]
	prevJob, prevDone := newWarmJob, warmDone
	newWarmJob = func() *pace.Job {
		return pace.New(pace.Sources{P95: func() (int64, bool) { return ms, true }}).NewJob(pace.JobConfig{Min: 1, Max: 64, Step: 4})
	}
	done := make(chan struct{})
	warmDone = done
	warmState.Lock()
	warmState.run = nil
	warmState.Unlock()
	t.Cleanup(func() {
		close(done)
		deadline := time.Now().Add(10 * time.Second)
		for run := warmProgress(); run != nil && run.Finished.IsZero(); run = warmProgress() {
			if time.Now().After(deadline) {
				t.Error("the refresh pass did not stop when the process did")
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		render.WaitForPurges() // the sitemap, feed and robots rebuilds it started
		newWarmJob, warmDone = prevJob, prevDone
		warmState.Lock()
		warmState.run = nil
		warmState.Unlock()
	})
}

// finished waits for the pass to end, or fails.
func finished(t *testing.T) *warmRun {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if run := warmProgress(); run != nil && !run.Finished.IsZero() {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("the refresh pass did not finish: %+v", warmProgress())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// refreshSite is a site with three published posts, one draft, and a cache
// holding pages for them, pages nobody can ask for any more, and files that
// are not pages at all.
func refreshSite(t *testing.T) (a *App, cache string) {
	t.Helper()
	setupRelatedTestDB(t)
	render.Init(t.TempDir())
	a = &App{siteSettings: settings.New(dbpkg.DB)}
	repo := dbpkg.NewArticleRepo(dbpkg.DB)
	now := time.Now()
	for _, p := range []struct{ slug, status string }{{"one", "published"}, {"two", "published"}, {"three", "published"}, {"draft", "draft"}} {
		if err := repo.Create(context.Background(), dbpkg.Article{ID: p.slug, Title: p.slug, Slug: p.slug, Content: "<p>x</p>",
			Status: p.status, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	cache = config.Cfg.CacheDir
	old := now.Add(-time.Hour)
	for _, rel := range []string{"posts/one.html", "posts/two.html", "posts/gone.html", "posts/draft.html",
		"d_retired/tags/x.html", "home/d_retired/index.html",
		"sitemap.xml", "update-backups/vp.db.pre-update", ".render-stamp-keep", "d_/x"} {
		p := filepath.Join(cache, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("<p>old</p>"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, old, old)
	}
	return a, cache
}

// Refresh every page deletes nothing that is still in use: every page is
// marked out of date and stays in place, so visitors keep a copy to be served
// while the new one is rendered. Deleting them made every request a database
// render at once (johal.in, 2026-09-26).
func TestRefreshMarksEveryPageStaleAndDeletesNone(t *testing.T) {
	a, cache := refreshSite(t)
	pacedBy(t, pace.Wait) // the pass may not rebuild or remove anything yet
	r := httptest.NewRequest(http.MethodPost, "/os/api/storage/refresh-pages", strings.NewReader(`{}`))
	r = r.WithContext(context.WithValue(r.Context(), ctxUserKey, &users.User{Role: users.RoleAdmin}))
	w := httptest.NewRecorder()
	start := time.Now()
	a.handleOSStorageRefreshPages(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("refresh answered %d %s", w.Code, w.Body.String())
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the refresh answered after %s; it must return at once and work in the background", d)
	}
	for _, rel := range []string{"posts/one.html", "posts/two.html", "posts/gone.html"} {
		fi, err := os.Stat(filepath.Join(cache, rel))
		if err != nil {
			t.Fatalf("%s was deleted by the refresh: a visitor has no copy to be served while it is rebuilt", rel)
		}
		if render.CacheEntryFresh(fi) {
			t.Errorf("%s is still fresh after Refresh every page", rel)
		}
	}
}

// While the pacer says wait, nothing is rebuilt or removed, and the Storage
// page says why.
func TestNothingIsRebuiltWhileThePacerSaysWait(t *testing.T) {
	a, cache := refreshSite(t)
	pacedBy(t, pace.Wait)
	render.CachePurgeAll()
	if joined := a.startRefresh(); joined {
		t.Fatal("the first refresh reported joining another")
	}
	time.Sleep(300 * time.Millisecond)
	run := warmProgress()
	if run.Rebuilt != 0 || run.Removed != 0 {
		t.Errorf("with the pacer saying wait, the pass rebuilt %d and removed %d", run.Rebuilt, run.Removed)
	}
	if _, err := os.Stat(filepath.Join(cache, "posts", "gone.html")); err != nil {
		t.Error("an orphan was removed while the pacer said wait")
	}
	if out := refreshStatusHTML(run); !strings.Contains(out, "Waiting") || !strings.Contains(out, "pages taking 5000 ms") {
		t.Errorf("the Storage page does not say the refresh is waiting, and why:\n%s", out)
	}
	// A second refresh joins the one running instead of starting another.
	if joined := a.startRefresh(); !joined {
		t.Error("a second refresh started a second pass")
	}
}

// With room to work, the pass rebuilds every published page and removes the
// pages nobody can ask for, and nothing else.
func TestTheRefreshRebuildsEveryPageAndRemovesOnlyOrphans(t *testing.T) {
	a, cache := refreshSite(t)
	pacedBy(t, pace.Go)
	render.CachePurgeAll()
	time.Sleep(10 * time.Millisecond) // rebuilt pages are written after the cutoff
	a.startRefresh()
	run := finished(t)

	for _, slug := range []string{"one", "two", "three"} {
		fi, err := os.Stat(filepath.Join(cache, "posts", slug+".html"))
		if err != nil || !render.CacheEntryFresh(fi) {
			t.Errorf("the published post %s was not rebuilt", slug)
		}
	}
	for _, rel := range []string{"posts/gone.html", "posts/draft.html", "d_retired/tags/x.html", "home/d_retired/index.html"} {
		if _, err := os.Stat(filepath.Join(cache, rel)); !os.IsNotExist(err) {
			t.Errorf("%s, a page nobody can ask for, survived the refresh", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(cache, "d_retired")); !os.IsNotExist(err) {
		t.Error("the retired domain's emptied directory was left behind")
	}
	for _, rel := range []string{"sitemap.xml", "update-backups/vp.db.pre-update", ".render-stamp-keep", "d_/x"} {
		if _, err := os.Stat(filepath.Join(cache, rel)); err != nil {
			t.Errorf("%s is not a page and was removed by the refresh", rel)
		}
	}
	if run.Removed != 4 || run.Freed != 4*int64(len("<p>old</p>")) {
		t.Errorf("the pass reports %d removed, %d bytes; want 4 and %d", run.Removed, run.Freed, 4*len("<p>old</p>"))
	}
	if out := refreshStatusHTML(run); !strings.Contains(out, "Last refresh finished") || !strings.Contains(out, "removed 4 pages whose post is gone") {
		t.Errorf("the Storage page does not report the finished refresh:\n%s", out)
	}
}

// The API's full purge is the same refresh: nothing deleted.
func TestTheAPIsFullPurgeRefreshesInsteadOfDeleting(t *testing.T) {
	a, cache := refreshSite(t)
	pacedBy(t, pace.Wait)
	w := httptest.NewRecorder()
	a.handleAdminCachePurge(w, httptest.NewRequest(http.MethodPost, "/api/v1/cache/purge", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("full purge answered %d %s", w.Code, w.Body.String())
	}
	fi, err := os.Stat(filepath.Join(cache, "posts", "one.html"))
	if err != nil {
		t.Fatal("the API's full purge deleted a page still in use")
	}
	if render.CacheEntryFresh(fi) {
		t.Error("the API's full purge left the page fresh")
	}
	var got struct {
		Purged    int    `json:"purged"`
		PurgeType string `json:"purge_type"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Purged != 3 || got.PurgeType != "full" {
		t.Errorf("full purge reports %s; want purge_type full and the three published posts it will rebuild", w.Body.String())
	}
}

// The sweep removes a retired domain's pages and keeps a registered one's.
func TestTheSweepKeepsARegisteredDomainsPages(t *testing.T) {
	a, cache := refreshSite(t)
	reg := domain.New(dbpkg.DB, dbpkg.RDB)
	if err := reg.EnsurePrimary(context.Background(), "example.test", domain.SiteBlog); err != nil {
		t.Fatal(err)
	}
	if _, err := dbpkg.DB.Exec(`INSERT INTO domains(id,host,site_type,is_primary,status,created_at,updated_at) VALUES('live','live.example','blog',0,'active',datetime('now'),datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	a.domains = reg
	for _, rel := range []string{"d_live/tags/y.html", "home/d_live/index.html"} {
		p := filepath.Join(cache, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	job := pace.New(pace.Sources{P95: func() (int64, bool) { return 10, true }}).NewJob(pace.JobConfig{Min: 64, Max: 64})
	budget := 0
	next := func() bool {
		if budget > 0 {
			return true
		}
		n, err := job.Next(context.Background())
		budget = n
		return err == nil
	}
	a.removeOrphanPages(context.Background(), next, &budget, &warmRun{})
	for _, rel := range []string{"d_live/tags/y.html", "home/d_live/index.html"} {
		if _, err := os.Stat(filepath.Join(cache, rel)); err != nil {
			t.Errorf("%s belongs to a registered domain and was removed", rel)
		}
	}
	for _, rel := range []string{"d_retired/tags/x.html", "home/d_retired/index.html"} {
		if _, err := os.Stat(filepath.Join(cache, rel)); !os.IsNotExist(err) {
			t.Errorf("%s belongs to a retired domain and survived", rel)
		}
	}
}
