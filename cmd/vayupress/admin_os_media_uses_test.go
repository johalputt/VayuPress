// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// forgetMediaUses waits for a scan off the request path to finish, then drops
// the kept one. The memo is package state: a scan left running reads the
// database pools as a test's cleanup closes them (a race the race detector
// caught), and a finished one would describe the last test's database to the
// next.
func forgetMediaUses() {
	for i := 0; i < 500; i++ {
		mediaUsesMemo.Lock()
		busy := mediaUsesMemo.refreshing
		mediaUsesMemo.Unlock()
		if !busy {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mediaUsesMemo.Lock()
	mediaUsesMemo.uses, mediaUsesMemo.at, mediaUsesMemo.refreshing = nil, time.Time{}, false
	mediaUsesMemo.Unlock()
}

// resetMediaUses starts a test from a first look. openMigratedDB forgets the
// memo again as it closes the database.
func resetMediaUses(t *testing.T) {
	t.Helper()
	forgetMediaUses()
}

type listedMedia struct {
	Items []struct {
		Name string           `json:"name"`
		Uses *json.RawMessage `json:"uses"`
	}
	Checked *string `json:"uses_checked"`
}

func listMedia(t *testing.T, a *App) listedMedia {
	t.Helper()
	rec := httptest.NewRecorder()
	a.handleOSMediaList(rec, httptest.NewRequest(http.MethodGet, "/os/api/media", nil))
	var out listedMedia
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("list: %v %s", err, rec.Body.String())
	}
	return out
}

// On johal.in the scan for where files are used reads 234,615 posts, which
// outlasted the request: the page never received its list and, on the first
// tap, said "No file matches that" over 55 files. The list now answers at once
// and says where files are used once a scan off the request path has finished;
// until then, and for any file newer than that scan, it does not know.
func TestTheLibraryAnswersBeforeItKnowsWhereFilesAreUsed(t *testing.T) {
	openMigratedDB(t)
	resetMediaUses(t)
	dir := mediaQuotaDir(t, 1<<20)
	for _, n := range []string{libA, libB} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, libA), past, past); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour) // written after any scan this test starts
	if err := os.Chtimes(filepath.Join(dir, libB), future, future); err != nil {
		t.Fatal(err)
	}
	a := &App{}

	first := listMedia(t, a)
	if len(first.Items) != 2 || first.Checked != nil {
		t.Fatalf("the first look lists %d files, checked %v; want both, not yet checked", len(first.Items), first.Checked)
	}
	for _, it := range first.Items {
		if it.Uses != nil && string(*it.Uses) != "null" {
			t.Errorf("%s is said to be used in %s before anything was checked", it.Name, *it.Uses)
		}
	}

	var later listedMedia
	for i := 0; i < 500; i++ {
		if later = listMedia(t, a); later.Checked != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if later.Checked == nil {
		t.Fatal("the scan never finished")
	}
	for _, it := range later.Items {
		got := "null"
		if it.Uses != nil {
			got = string(*it.Uses)
		}
		switch it.Name {
		case libA:
			if got != "[]" {
				t.Errorf("a file the scan covered and found nowhere reads %s, want []", got)
			}
		case libB:
			if got != "null" {
				t.Errorf("a file newer than the scan reads %s; it has not been looked for", got)
			}
		}
	}
}

// A scan that fails keeps the last complete one: a partial scan would call
// files unused that a later post still shows, and the trash confirmation
// would repeat it.
func TestAScanThatFailsKeepsTheLastCompleteOne(t *testing.T) {
	openMigratedDB(t)
	resetMediaUses(t)
	a := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.mediaUsage(ctx); err == nil {
		t.Fatal("a scan with its context cancelled reported success")
	}

	kept := map[string][]mediaUse{libA: {{Label: "Post · Kept", Href: "/os/editor/kept"}}}
	at := time.Now().Add(-time.Hour)
	mediaUsesMemo.Lock()
	mediaUsesMemo.uses, mediaUsesMemo.at = kept, at
	mediaUsesMemo.Unlock()

	prevDB, prevRDB := dbpkg.DB, dbpkg.RDB
	broken, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "gone.db"))
	if err != nil {
		t.Fatal(err)
	}
	broken.Close()
	dbpkg.DB, dbpkg.RDB = broken, nil
	a.refreshMediaUses()
	dbpkg.DB, dbpkg.RDB = prevDB, prevRDB

	mediaUsesMemo.Lock()
	defer mediaUsesMemo.Unlock()
	if !mediaUsesMemo.at.Equal(at) || len(mediaUsesMemo.uses[libA]) != 1 {
		t.Fatalf("a failed scan replaced the kept one: at %v, uses %v", mediaUsesMemo.at, mediaUsesMemo.uses)
	}
}

// A file added after the last scan starts another, rather than reading
// "Checking…" until the kept scan is old: the upload's own row said so for up
// to five minutes.
func TestAFileNewerThanTheScanStartsAnother(t *testing.T) {
	openMigratedDB(t)
	resetMediaUses(t)
	dir := mediaQuotaDir(t, 1<<20)
	name := filepath.Join(dir, libA)
	if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	added := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(name, added, added); err != nil {
		t.Fatal(err)
	}
	// A scan well inside the TTL, begun before the file was added.
	mediaUsesMemo.Lock()
	mediaUsesMemo.uses, mediaUsesMemo.at = map[string][]mediaUse{}, added.Add(-10*time.Second)
	mediaUsesMemo.Unlock()
	a := &App{}

	for i := 0; i < 500; i++ {
		l := listMedia(t, a)
		if len(l.Items) == 1 && l.Items[0].Uses != nil && string(*l.Items[0].Uses) == "[]" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("a file added after the kept scan was never looked for")
}

// A file dated in the future is newer than any scan could be, so counting it
// would start a scan of every post on every look at the library.
func TestAFileDatedInTheFutureStartsNoScan(t *testing.T) {
	openMigratedDB(t)
	resetMediaUses(t)
	dir := mediaQuotaDir(t, 1<<20)
	name := filepath.Join(dir, libB)
	if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(name, future, future); err != nil {
		t.Fatal(err)
	}
	mediaUsesMemo.Lock()
	mediaUsesMemo.uses, mediaUsesMemo.at = map[string][]mediaUse{}, time.Now().Add(-time.Minute)
	mediaUsesMemo.Unlock()

	listMedia(t, &App{})
	mediaUsesMemo.Lock()
	defer mediaUsesMemo.Unlock()
	if mediaUsesMemo.refreshing {
		t.Fatal("a file dated in the future started a scan inside the TTL")
	}
}
