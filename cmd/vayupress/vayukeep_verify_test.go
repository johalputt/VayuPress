// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

type askCounter struct{ n int }

func (c *askCounter) Next(ctx context.Context) (int, error) { c.n++; return 1, ctx.Err() }

// restoredFixture is a database the drill might restore: articles, and a
// second table with an index, big enough to span many pages.
func restoredFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "restored.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE articles(id INTEGER PRIMARY KEY, title TEXT)`,
		`CREATE TABLE trail(id INTEGER PRIMARY KEY, ip TEXT, body TEXT)`,
		`CREATE INDEX trail_ip ON trail(ip)`,
		`INSERT INTO articles(title) VALUES ('one'), ('two'), ('three')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	tx, _ := db.Begin()
	for i := range 2000 {
		if _, err := tx.Exec(`INSERT INTO trail(ip, body) VALUES (?, ?)`, "10.0.0."+strings.Repeat("9", i%3+1), strings.Repeat("x", 300)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return path
}

// The test restore checks each table on its own, asking the pacer before each,
// and still counts the posts that make "restored" mean "restored something".
func TestTheTestRestoreChecksEachTableAtThePacersWord(t *testing.T) {
	path := restoredFixture(t)
	p := &askCounter{}
	n, err := verifyRestoredDB(context.Background(), path, p)
	if err != nil || n != 3 {
		t.Fatalf("verifyRestoredDB = %d, %v; want 3 posts, no error", n, err)
	}
	if p.n != 2 {
		t.Errorf("the pacer was asked %d times for 2 tables", p.n)
	}
}

// A damaged table fails the check, and the failure names the table, which is
// what an operator reading "test restore FAILED" needs next.
func TestADamagedTableFailsTheTestRestoreByName(t *testing.T) {
	path := restoredFixture(t)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	var root, pageSize int
	if err := db.QueryRow(`SELECT rootpage FROM sqlite_schema WHERE name = 'trail_ip'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	db.Close()
	f, err := os.OpenFile(path, os.O_RDWR, 0) // #nosec G304 -- test fixture
	if err != nil {
		t.Fatal(err)
	}
	// Scramble the index's root page past its header: the table's rows are
	// intact, its index no longer agrees with them.
	if _, err := f.WriteAt([]byte(strings.Repeat("\xff", pageSize-16)), int64(root-1)*int64(pageSize)+16); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, err = verifyRestoredDB(context.Background(), path, &askCounter{})
	if err == nil || !strings.Contains(err.Error(), "trail") {
		t.Fatalf("a damaged index on trail gave %v; want a failure naming trail", err)
	}
}

// The check runs on the schema the install really has: every table the
// migrations create passes a per-table integrity_check, so the drill of a
// genuine backup cannot fail on a table the check does not understand.
func TestTheTestRestoreChecksTheRealSchema(t *testing.T) {
	openMigratedDB(t)
	var tables int
	if err := dbpkg.DB.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	p := &askCounter{}
	if _, err := verifyRestoredDB(context.Background(), config.Cfg.DBPath, p); err != nil {
		t.Fatalf("a freshly migrated database fails the test restore's check: %v", err)
	}
	if p.n != tables || tables < 50 {
		t.Errorf("checked %d of %d tables", p.n, tables)
	}
}

// A copy the server has kept at its slowest for an hour is worth a word in the
// bell; less than that is ordinary pacing and says nothing.
func TestTheBellSpeaksUpForABackupHeldAnHour(t *testing.T) {
	now := time.Date(2026, 9, 26, 15, 0, 0, 0, time.UTC)
	if _, ok := keepHeldNotice(now.Add(-59*time.Minute), "disk stalled 41% of the last 10 s", now); ok {
		t.Error("the bell spoke up for a copy held under an hour")
	}
	n, ok := keepHeldNotice(now.Add(-61*time.Minute), "disk stalled 41% of the last 10 s", now)
	if !ok || n.Severity != "warn" || !strings.Contains(n.Detail, "since 13:59 UTC: disk stalled 41%") {
		t.Errorf("a copy held 61 minutes gave %+v, %v", n, ok)
	}
}
