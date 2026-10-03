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
	"sync"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/users"
	"github.com/johalputt/vayupress/internal/vayukeep"
)

// storageLayout points the Storage page at a temporary install whose backup
// directory is also Backups' target, as it is on a default install, and
// returns the app and that directory.
func storageLayout(t *testing.T) (*App, string, string) {
	t.Helper()
	prev := config.Cfg
	t.Cleanup(func() { config.Cfg = prev })
	root := t.TempDir()
	data, backups, logs := filepath.Join(root, "data"), filepath.Join(root, "backups"), filepath.Join(root, "logs")
	for _, d := range []string{data, backups, logs, filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	config.Cfg.DBPath = filepath.Join(data, "vayupress.db")
	config.Cfg.CacheDir = filepath.Join(root, "cache")
	config.Cfg.TmpDir = filepath.Join(root, "tmp")
	t.Setenv("VAYU_BACKUP_DIR", backups)
	t.Setenv("VAYU_LOG_DIR", logs)
	mustWrite(t, config.Cfg.DBPath, "live")
	e, err := vayukeep.New(vayukeep.Config{
		Enabled: true, DataDir: data, DBPath: config.Cfg.DBPath,
		TargetDir: backups, Passphrase: "a test passphrase", Log: func(string, string) {},
		Snapshot: func(context.Context, string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return &App{vayuKeep: e}, backups, logs
}

type storageDeleteReply struct {
	Deleted int      `json:"deleted"`
	Removed []string `json:"removed"`
	Failed  []string `json:"failed"`
}

func storageDelete(t *testing.T, a *App, paths ...string) storageDeleteReply {
	t.Helper()
	body, _ := json.Marshal(map[string][]string{"paths": paths})
	r := withUser(httptest.NewRequest(http.MethodPost, "/os/api/storage/delete", strings.NewReader(string(body))), &users.User{Role: users.RoleAdmin})
	w := httptest.NewRecorder()
	a.handleOSStorageDelete(w, r)
	var got storageDeleteReply
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	return got
}

func fileRow(t *testing.T, a *App, name string) string {
	t.Helper()
	for _, row := range strings.Split(storageFilesTable(a.managedStorageFiles()), "<tr data-file-row>")[1:] {
		if strings.Contains(row, `class="row-title">`+name+`<`) {
			return row
		}
	}
	t.Fatalf("%s is not listed", name)
	return ""
}

// Storage lists Backups' restore points beside everything else, and its Delete
// looked the same on the last one as on any other. Asked to delete both of two
// at once, it removes one and refuses the last, saying why, by Backups' rule;
// the last is then shown as kept, with nothing to select or press.
func TestStorageNeverDeletesTheOnlyRestorePoint(t *testing.T) {
	a, backups, _ := storageLayout(t)
	older := filepath.Join(backups, "vk-20261002-090000.vpbk")
	newest := filepath.Join(backups, "vk-20261003-114512.vpbk")
	mustWrite(t, older, "sealed")
	mustWrite(t, newest, "sealed")

	if row := fileRow(t, a, filepath.Base(newest)); !strings.Contains(row, "data-file-delete") || !strings.Contains(row, fileRestorePoint) {
		t.Fatalf("one of two restore points is offered as %s", row)
	}
	got := storageDelete(t, a, older, newest)
	if len(got.Removed) != 1 || got.Removed[0] != older {
		t.Errorf("removed %v, want only %s", got.Removed, older)
	}
	if len(got.Failed) != 1 || got.Failed[0] != filepath.Base(newest)+": "+onlyRestorePointRefusal {
		t.Errorf("refused %q", got.Failed)
	}
	if _, err := os.Stat(newest); err != nil {
		t.Fatalf("the last restore point was deleted: %v", err)
	}
	row := fileRow(t, a, filepath.Base(newest))
	if strings.Contains(row, "data-file-delete") || strings.Contains(row, "data-file-select") || !strings.Contains(row, "Your only restore point") {
		t.Errorf("the only restore point is offered as %s", row)
	}
}

// Backups and Storage delete restore points through the one rule: the page
// that had it keeps it.
func TestBackupsNeverDeletesTheOnlyRestorePoint(t *testing.T) {
	a, backups, _ := storageLayout(t)
	only := filepath.Join(backups, "vk-20261003-114512.vpbk")
	mustWrite(t, only, "sealed")
	r := withUser(httptest.NewRequest(http.MethodPost, "/os/api/vayukeep/delete", strings.NewReader(`{"name":"vk-20261003-114512.vpbk"}`)), &users.User{Role: users.RoleAdmin})
	w := httptest.NewRecorder()
	a.handleOSVayuKeepDelete(w, r)
	if !strings.Contains(w.Body.String(), onlyRestorePointRefusal) {
		t.Errorf("Backups answered %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(only); err != nil {
		t.Fatalf("Backups deleted the only restore point: %v", err)
	}
}

// The July file on johal.in, a whole unencrypted copy of the database left by
// an update, was listed as a "Database backup" exactly like the encrypted,
// tested restore point beside it.
func TestStorageNamesADatabaseCopyApartFromARestorePoint(t *testing.T) {
	a, backups, _ := storageLayout(t)
	july := filepath.Join(filepath.Dir(config.Cfg.DBPath), "vayupress.backup-20260705-135041.db")
	mustWrite(t, july, "SQLite format 3")
	mustWrite(t, filepath.Join(backups, "vk-20261003-114512.vpbk"), "sealed")

	if row := fileRow(t, a, filepath.Base(july)); !strings.Contains(row, `<span class="chip">Database copy</span>`) || !strings.Contains(row, "data-file-delete") {
		t.Errorf("the pre-update copy is listed as %s", row)
	}
	if row := fileRow(t, a, "vk-20261003-114512.vpbk"); !strings.Contains(row, `<span class="chip">Restore point</span>`) {
		t.Errorf("the restore point is listed as %s", row)
	}
}

// On an install still running the old unit, systemd appends the server's
// output to a log file in the log directory. Deleting it frees nothing until a
// restart and loses every line after. The one this process writes to is kept;
// the rotated one beside it, which nothing writes, is not.
func TestStorageKeepsTheLogThisServerWrites(t *testing.T) {
	a, _, logs := storageLayout(t)
	live := filepath.Join(logs, "vayupress.log")
	rotated := filepath.Join(logs, "vayupress.log.1")
	mustWrite(t, rotated, "old lines")
	out, err := os.OpenFile(live, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- test fixture
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	prev := os.Stdout
	os.Stdout = out
	t.Cleanup(func() { os.Stdout = prev })

	if row := fileRow(t, a, "vayupress.log"); strings.Contains(row, "data-file-delete") || !strings.Contains(row, "Being written") {
		t.Errorf("the log being written is offered as %s", row)
	}
	got := storageDelete(t, a, live, rotated)
	if len(got.Failed) != 1 || got.Failed[0] != "vayupress.log: "+writingLogRefusal {
		t.Errorf("refused %q", got.Failed)
	}
	if len(got.Removed) != 1 || got.Removed[0] != rotated {
		t.Errorf("removed %v, want only %s", got.Removed, rotated)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("the log being written was deleted: %v", err)
	}
}

// Backups in one tab and Storage in another, each deleting one of the last two
// restore points at the same moment, must leave one: each delete lists the
// target, so without one at a time both see the other's file still there.
func TestTwoDeletesAtOnceLeaveARestorePoint(t *testing.T) {
	a, backups, _ := storageLayout(t)
	names := []string{"vk-20261002-090000.vpbk", "vk-20261003-114512.vpbk"}
	for round := 0; round < 200; round++ {
		for _, n := range names {
			mustWrite(t, filepath.Join(backups, n), "sealed")
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, n := range names {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, _ = a.deleteRestorePoint(n)
			}()
		}
		close(start)
		wg.Wait()
		if gens, _ := a.vayuKeep.List(); len(gens) == 0 {
			t.Fatalf("round %d: two deletes at once removed both restore points", round)
		}
	}
}
