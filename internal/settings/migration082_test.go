// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// Migration 082 rebuilds site_settings so the primary key carries the scope.
//
// This test exists because a mutation caught its absence: changing the
// migration's backfill target from '' to 'legacy' broke nothing, since every
// other test writes through the Store against an already-empty table and the
// backfill statement was never exercised with data in it.
//
// That is the one statement an existing install depends on. Every setting an
// operator has ever chosen — their theme, their site name, their SEO defaults —
// is in the old table, and if the copy drops those rows or files them under a
// scope nothing reads, the install comes back up looking like a fresh one. It
// would look exactly like a working upgrade until somebody loaded the site.

// migrate082Fixture builds the PRE-082 table, fills it, and runs the shipped
// migration over it — the real upgrade path, statement for statement.
func migrate082Fixture(t *testing.T, rows map[string]string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // ":memory:" is per-connection
	t.Cleanup(func() { _ = db.Close() })

	execShipped(t, db, "../db/migrations/010-site-settings.up.sql")
	for k, v := range rows {
		if _, err := db.Exec(`INSERT INTO site_settings(key,value) VALUES(?,?)`, k, v); err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}
	execShipped(t, db, "../db/migrations/082-settings-scope.up.sql")
	return db
}

func execShipped(t *testing.T, db *sql.DB, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	// The migration runner executes ONE statement per physical LINE.
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		if _, err := db.Exec(line); err != nil {
			t.Fatalf("%s: %v (%s)", path, err, line)
		}
	}
}

// Every pre-existing row must survive, with its value, filed under the primary.
func TestMigration082PreservesEveryExistingSettingUnderThePrimary(t *testing.T) {
	seed := map[string]string{
		KeySiteName:         "My Established Blog",
		KeySiteTagline:      "eleven years of writing",
		KeyThemePrimaryDark: "#123456",
		KeyThemeCustomCSS:   "body{letter-spacing:.01em}",
	}
	db := migrate082Fixture(t, seed)

	var total int
	if err := db.QueryRow(`SELECT COUNT(1) FROM site_settings`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != len(seed) {
		t.Fatalf("%d row(s) survived the rebuild, want %d — the operator's configuration was "+
			"lost by an upgrade that reported success", total, len(seed))
	}

	for k, want := range seed {
		var got, scope string
		if err := db.QueryRow(`SELECT value, scope FROM site_settings WHERE key=?`, k).Scan(&got, &scope); err != nil {
			t.Errorf("%s vanished in the rebuild: %v", k, err)
			continue
		}
		if got != want {
			t.Errorf("%s = %q after migration, want %q", k, got, want)
		}
		if scope != "" {
			t.Errorf("%s landed under scope %q, want \"\" (the primary). Filed anywhere else, "+
				"the store reads defaults and the install comes back looking brand new", k, scope)
		}
	}

	// And the store — the thing that actually serves them — must find them.
	s := New(db)
	for k, want := range seed {
		if got := s.Get(t.Context(), ForPrimary(), k); got != want {
			t.Errorf("after upgrade the store reads %s = %q, want %q", k, got, want)
		}
	}
}

// The rebuild must leave no scaffolding behind. A surviving pre082 table is a
// second copy of every setting, which will be stale within a day and is exactly
// the sort of thing a later migration or an operator's backup restores from.
func TestMigration082LeavesNoTemporaryTable(t *testing.T) {
	db := migrate082Fixture(t, map[string]string{KeySiteName: "x"})
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='site_settings_pre082'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("site_settings_pre082 survived the migration — a stale duplicate of every setting")
	}
}

// After the rebuild, two scopes must be able to hold the same key. This is the
// property the new primary key exists for; if the migration produced the old
// shape, everything above still passes and nothing is actually per-domain.
func TestAfterMigration082TwoScopesCanHoldTheSameKey(t *testing.T) {
	db := migrate082Fixture(t, map[string]string{KeySiteName: "The Studio"})
	if _, err := db.Exec(
		`INSERT INTO site_settings(scope,key,value) VALUES(?,?,?)`,
		"client.example", KeySiteName, "Client Ltd"); err != nil {
		t.Fatalf("a second scope could not hold the same key: %v\n"+
			"The rebuilt table still keys on (key) alone, so the migration ran and changed "+
			"nothing that matters", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM site_settings WHERE key=?`, KeySiteName).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("%d row(s) for one key across two scopes, want 2", n)
	}
}
