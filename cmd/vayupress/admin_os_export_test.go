// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/update"
	"github.com/johalputt/vayupress/internal/users"
)

// Behind a reverse proxy a large site's download takes longer to build than the
// proxy waits for a first byte, so the console prepares it as background work
// and then offers it. What it offers is the whole database, and a second
// download replaces the first rather than leaving a copy of the site behind.
func TestTheConsoleDownloadIsPreparedThenOffered(t *testing.T) {
	prevCfg, prevDB, prevRDB, prevPacing := config.Cfg, dbpkg.DB, dbpkg.RDB, updatePacing
	t.Cleanup(func() { config.Cfg, dbpkg.DB, dbpkg.RDB, updatePacing = prevCfg, prevDB, prevRDB, prevPacing })
	updatePacing = func() update.Pacing { return update.Pacing{} } // the host pacer waits on a busy runner

	dir := t.TempDir()
	config.Cfg.DBPath = filepath.Join(dir, "site.db")
	config.Cfg.TmpDir = filepath.Join(dir, "tmp")
	if err := os.MkdirAll(config.Cfg.TmpDir, 0o750); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", config.Cfg.DBPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE articles(id TEXT PRIMARY KEY, title TEXT); INSERT INTO articles VALUES('a1','kept');
		CREATE TABLE audit_log(ts DATETIME, action TEXT, actor TEXT, target TEXT, detail TEXT)`); err != nil {
		t.Fatal(err)
	}
	dbpkg.DB, dbpkg.RDB = db, nil

	a := &App{} // automatic backup is not set up: the download does not need it
	admin := &users.User{Role: users.RoleAdmin}
	press := func() {
		t.Helper()
		w := httptest.NewRecorder()
		a.handleOSBackupExportStart(w, withUser(httptest.NewRequest(http.MethodPost, "/os/api/backup/export/start", strings.NewReader(`{}`)), admin))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok": true`) {
			t.Fatalf("start answered %d %s", w.Code, w.Body.String())
		}
	}
	fetch := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.handleOSBackupExportFile(w, withUser(httptest.NewRequest(http.MethodGet, "/os/api/backup/export/file", nil), admin))
		return w
	}

	if w := fetch(); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "No download is waiting") {
		t.Fatalf("with nothing prepared the file answered %d %s", w.Code, w.Body.String())
	}

	// A build that fails leaves no partial archive behind in TMP_DIR.
	live := config.Cfg.DBPath
	config.Cfg.DBPath = filepath.Join(dir, "missing.db")
	if _, _, err := buildExport(t.Context(), update.Pacing{}); err == nil {
		t.Fatal("an export of a missing database reported success")
	}
	config.Cfg.DBPath = live
	if left, _ := filepath.Glob(filepath.Join(config.Cfg.TmpDir, exportTempPattern)); len(left) != 0 {
		t.Fatalf("a failed export left %v", left)
	}

	press()
	if out := keepOutcome(t, a); !strings.Contains(out, `href="/os/api/backup/export/file"`) {
		t.Fatalf("the prepared download is not offered: %s", out)
	}
	// The poll the page makes works without automatic backup set up.
	poll := httptest.NewRecorder()
	a.handleOSVayuKeepRun(poll, withUser(httptest.NewRequest(http.MethodGet, "/os/vayukeep/run", nil), admin))
	if poll.Code != http.StatusOK {
		t.Fatalf("the Running now poll answered %d without automatic backup", poll.Code)
	}

	w := fetch()
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("download answered %d, disposition %q", w.Code, w.Header().Get("Content-Disposition"))
	}
	gz, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var got []byte
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "vayupress.db" {
			got, _ = io.ReadAll(tr)
		}
	}
	restored := filepath.Join(dir, "restored.db")
	if err := os.WriteFile(restored, got, 0o600); err != nil {
		t.Fatal(err)
	}
	rdb, err := sql.Open("sqlite3", restored)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	var title string
	if err := rdb.QueryRow(`SELECT title FROM articles WHERE id='a1'`).Scan(&title); err != nil || title != "kept" {
		t.Fatalf("the download does not hold the site's rows: %q %v", title, err)
	}

	first := a.keepRun.download()
	press()
	keepOutcome(t, a)
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("the first download is still on the server after a second was prepared: %v", err)
	}
}
