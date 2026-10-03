// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/users"
	"github.com/johalputt/vayupress/internal/vayukeep"
)

// coverageLayout is an install as a standard deploy lays it out: the
// database's folder holds media, the website beside it, and mail and PGP
// under vayudata. It returns the data folder.
func coverageLayout(t *testing.T) string {
	t.Helper()
	prev := config.Cfg
	t.Cleanup(func() { config.Cfg = prev })
	t.Setenv("VAYU_SECRET_KEK_FILE", "")
	t.Setenv("VAYUOS_TOR_DIR", "")
	data := filepath.Join(t.TempDir(), "data")
	config.Cfg.DBPath = filepath.Join(data, "vayupress.db")
	config.Cfg.MediaDir = filepath.Join(data, "media")
	put(t, config.Cfg.DBPath, "db")
	return data
}

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func placeNamed(t *testing.T, data, label string) backupPlace {
	t.Helper()
	for _, p := range backupPlaces(data) {
		if p.Label == label {
			return p
		}
	}
	t.Fatalf("no place %q", label)
	return backupPlace{}
}

// Each way a place stands to a backup, one seed apiece.
func TestWhereABackupStandsToEachPlace(t *testing.T) {
	t.Run("held", func(t *testing.T) {
		data := coverageLayout(t)
		put(t, filepath.Join(data, "media", "a.jpg"), "x")
		if p := placeNamed(t, data, "Media library"); p.State != placeHeld || p.Rel != "media" {
			t.Errorf("media inside the data folder reads %s at %q", p.State, p.Rel)
		}
	})
	t.Run("nothing there", func(t *testing.T) {
		data := coverageLayout(t)
		if err := os.MkdirAll(filepath.Join(data, "media", "empty"), 0o750); err != nil {
			t.Fatal(err)
		}
		if p := placeNamed(t, data, "Media library"); p.State != placeEmpty {
			t.Errorf("a media folder with no file reads %s", p.State)
		}
	})
	t.Run("linked", func(t *testing.T) {
		data := coverageLayout(t)
		elsewhere := filepath.Join(t.TempDir(), "big-disk")
		put(t, filepath.Join(elsewhere, "a.jpg"), "x")
		if err := os.Symlink(elsewhere, filepath.Join(data, "media")); err != nil {
			t.Fatal(err)
		}
		if p := placeNamed(t, data, "Media library"); p.State != placeLinked {
			t.Errorf("media reached through a link reads %s", p.State)
		}
	})
	t.Run("linked, empty", func(t *testing.T) {
		data := coverageLayout(t)
		elsewhere := t.TempDir()
		if err := os.Symlink(elsewhere, filepath.Join(data, "media")); err != nil {
			t.Fatal(err)
		}
		if p := placeNamed(t, data, "Media library"); p.State != placeEmpty {
			t.Errorf("an empty folder behind a link reads %s", p.State)
		}
	})
	t.Run("outside", func(t *testing.T) {
		data := coverageLayout(t)
		config.Cfg.MediaDir = filepath.Join(t.TempDir(), "media")
		put(t, filepath.Join(config.Cfg.MediaDir, "a.jpg"), "x")
		if p := placeNamed(t, data, "Media library"); p.State != placeOutside {
			t.Errorf("media outside the data folder reads %s", p.State)
		}
	})
	t.Run("outside, empty", func(t *testing.T) {
		data := coverageLayout(t)
		config.Cfg.MediaDir = filepath.Join(t.TempDir(), "media")
		if err := os.MkdirAll(config.Cfg.MediaDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if p := placeNamed(t, data, "Media library"); p.State != placeEmpty {
			t.Errorf("an empty media folder outside reads %s", p.State)
		}
	})
	// A data folder that is itself a link is resolved by the archive, so the
	// places inside it are held, whichever way the configuration names them.
	t.Run("data folder is a link", func(t *testing.T) {
		coverageLayout(t)
		real := filepath.Join(t.TempDir(), "real")
		put(t, filepath.Join(real, "media", "a.jpg"), "x")
		link := filepath.Join(t.TempDir(), "data")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		config.Cfg.MediaDir = filepath.Join(real, "media")
		if p := placeNamed(t, link, "Media library"); p.State != placeHeld || p.Rel != "media" {
			t.Errorf("media named by the real path reads %s at %q", p.State, p.Rel)
		}
		config.Cfg.MediaDir = filepath.Join(link, "media")
		if p := placeNamed(t, link, "Media library"); p.State != placeHeld {
			t.Errorf("media named through the linked data folder reads %s", p.State)
		}
	})
}

// The test restore counts what it restored beyond the database, and fails a
// backup that lacks a place this server keeps data in.
func TestTheTestRestoreCountsMailMediaAndTheWebsite(t *testing.T) {
	data := coverageLayout(t)
	put(t, filepath.Join(data, "media", "a.jpg"), "x")
	put(t, filepath.Join(data, "custom-site", "index.html"), "x")
	put(t, filepath.Join(data, "vayudata", "mail", "maildir", "priya", "cur", "1:2,S"), "m")
	put(t, filepath.Join(data, "vayudata", "mail", "maildir", "priya", ".Sent", "new", "2"), "m")

	restored := t.TempDir()
	put(t, filepath.Join(restored, "media", "a.jpg"), "x")
	put(t, filepath.Join(restored, "custom-site", "index.html"), "x")
	put(t, filepath.Join(restored, "vayudata", "mail", "maildir", "priya", "cur", "1:2,S"), "m")
	put(t, filepath.Join(restored, "vayudata", "mail", "maildir", "priya", ".Sent", "new", "2"), "m")
	put(t, filepath.Join(restored, "vayudata", "mail", "maildir", "priya", "dovecot-uidlist"), "index")

	got, err := backupCensus(data)(context.Background(), restored)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1 media file, 1 website file, 2 mail messages" {
		t.Errorf("the test restore reports %q", got)
	}

	if err := os.RemoveAll(filepath.Join(restored, "vayudata", "mail")); err != nil {
		t.Fatal(err)
	}
	if _, err := backupCensus(data)(context.Background(), restored); err == nil || !strings.Contains(err.Error(), "it holds no mail, though this server has some") {
		t.Errorf("a backup without the server's mail passed: %v", err)
	}
}

// Mail set up with no message yet holds only its keys. That is present, and
// restored, and the test restore passes it as no messages rather than as mail
// missing from the backup.
func TestMailWithNoMessageYetPasses(t *testing.T) {
	data := coverageLayout(t)
	put(t, filepath.Join(data, "vayudata", "mail", "dkim", "johal.in.key"), "k")
	restored := t.TempDir()
	put(t, filepath.Join(restored, "vayudata", "mail", "dkim", "johal.in.key"), "k")
	got, err := backupCensus(data)(context.Background(), restored)
	if err != nil || got != "0 mail messages" {
		t.Errorf("mail with no message yet reads %q, %v", got, err)
	}
}

// A place a backup cannot hold (here, a link) is not demanded of the restored
// copy: Backups names it instead of failing every test restore over it.
func TestTheTestRestoreDoesNotDemandWhatABackupCannotHold(t *testing.T) {
	data := coverageLayout(t)
	elsewhere := filepath.Join(t.TempDir(), "big-disk")
	put(t, filepath.Join(elsewhere, "a.jpg"), "x")
	if err := os.Symlink(elsewhere, filepath.Join(data, "media")); err != nil {
		t.Fatal(err)
	}
	if _, err := backupCensus(data)(context.Background(), t.TempDir()); err != nil {
		t.Errorf("the test restore demanded linked media: %v", err)
	}
}

// The engine the install runs is given the census, over the database's folder.
func TestTheInstallsBackupsRunTheCensus(t *testing.T) {
	data := coverageLayout(t)
	put(t, filepath.Join(data, "media", "a.jpg"), "x")
	config.Cfg.VayuKeepTarget = filepath.Join(t.TempDir(), "replica")
	cfg := (&App{}).buildKeepConfig(context.Background())
	if cfg.Census == nil {
		t.Fatal("the install's backups run no census")
	}
	if _, err := cfg.Census(context.Background(), t.TempDir()); err == nil {
		t.Error("the install's census passed a restored copy with none of the server's media")
	}
}

// A backup that restores but leaves out a place with data is "Partial" on the
// page and in the bell, never "Protected"; the page says which and why.
func TestABackupThatLeavesDataOutIsPartial(t *testing.T) {
	data := coverageLayout(t)
	config.Cfg.MediaDir = filepath.Join(t.TempDir(), "media")
	put(t, filepath.Join(config.Cfg.MediaDir, "a.jpg"), "x")
	st := vayukeep.Status{Enabled: true, Target: "/mnt/replica", NewestGen: vkNow, LastDrill: vkNow, LastDrillOK: true, Generations: 1}
	places := backupPlaces(data)

	if v := keepStatusVerdict(st, "", vkNow, leftOut(places)); v.Chip != "Partial" || !strings.Contains(string(v.Detail), "Not in it: media.") {
		t.Errorf("the verdict with media left out is %q: %s", v.Chip, v.Detail)
	}
	if n, ok := backupNotification(st, "", vkNow, leftOut(places)); !ok || n.Detail != "Partial" {
		t.Errorf("the bell says nothing of media left out: %v %+v", ok, n)
	}
	page := osVayuKeepBody("n", st, "", nil, "", vkNow, "", false, keepPrefs{}, "", places)
	if !strings.Contains(page, "What a backup holds") || !strings.Contains(page, "Not backed up") ||
		!strings.Contains(page, "is outside "+data+", the folder a backup holds.") {
		t.Error("the page does not say media is outside the backup, and why")
	}
}

// What no backup holds is said every time; VAYU_SECRET only where it is set.
func TestThePageNamesWhatNoBackupHolds(t *testing.T) {
	data := coverageLayout(t)
	st := vayukeep.Status{Enabled: true, Target: "/mnt/replica", NewestGen: vkNow, LastDrill: vkNow, LastDrillOK: true, Generations: 1}
	t.Setenv("VAYU_SECRET", "")
	page := osVayuKeepBody("n", st, "", nil, "", vkNow, "", false, keepPrefs{}, "", backupPlaces(data))
	for _, want := range []string{"Start-up settings", "Backup passphrase", "Talk"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not name %s", want)
		}
	}
	if strings.Contains(page, ">VAYU_SECRET<") {
		t.Error("the page names VAYU_SECRET on an install that does not set it")
	}
	t.Setenv("VAYU_SECRET", "s3cret")
	if page := osVayuKeepBody("n", st, "", nil, "", vkNow, "", false, keepPrefs{}, "", backupPlaces(data)); !strings.Contains(page, ">VAYU_SECRET<") {
		t.Error("the page does not name VAYU_SECRET on an install that sets it")
	}
}

// The census's counts reach the page's line on the last test restore.
func TestTheLastTestRestoreSaysWhatItReadBack(t *testing.T) {
	st := vayukeep.Status{LastDrill: vkNow, LastDrillOK: true, LastDrillRows: 3, LastDrillContents: "2 mail messages"}
	if got := drillSummary(st, vkNow); !strings.Contains(got, "(3 posts, 2 mail messages read back)") {
		t.Errorf("the last test restore reads %q", got)
	}
}

// Through the install's own paths: an engine whose last test restore passed,
// with media kept outside the data folder, reads Partial on the Backups page
// and in the bell.
func TestTheInstallSaysWhenABackupLeavesMediaOut(t *testing.T) {
	data := coverageLayout(t)
	config.Cfg.MediaDir = filepath.Join(t.TempDir(), "media")
	put(t, filepath.Join(config.Cfg.MediaDir, "a.jpg"), "x")
	target := filepath.Join(t.TempDir(), "replica")
	gen := filepath.Join(target, "vk-20261003-114512.vpbk")
	put(t, gen, "sealed")
	fi, _ := os.Stat(gen)
	put(t, filepath.Join(target, vayukeep.DrillRecordName), fmt.Sprintf(
		`{"at":"2026-10-03T11:50:00Z","ok":true,"rows":3,"generation":"vk-20261003-114512.vpbk","proven":{"name":"vk-20261003-114512.vpbk","bytes":%d,"modified":%d}}`,
		fi.Size(), fi.ModTime().UnixNano()))
	e, err := vayukeep.New(vayukeep.Config{Enabled: true, DataDir: data, DBPath: config.Cfg.DBPath, TargetDir: target,
		Passphrase: "a test passphrase", Snapshot: func(context.Context, string, string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.Run(ctx)
	a := &App{vayuKeep: e}

	found := false
	for _, n := range a.osNotifications(context.Background(), &osSettings{AccessLevel: accessAdmin}) {
		found = found || (n.Href == "/os/vayukeep" && n.Detail == "Partial")
	}
	if !found {
		t.Error("the bell does not say the backups leave media out")
	}
	w := httptest.NewRecorder()
	a.handleOSVayuKeep(w, withUser(httptest.NewRequest(http.MethodGet, "/os/vayukeep", nil), &users.User{Role: users.RoleAdmin}))
	if body := w.Body.String(); !strings.Contains(body, "but not all of it") || !strings.Contains(body, "is outside "+data) {
		t.Errorf("the Backups page does not say media is left out (%d)", w.Code)
	}
}
