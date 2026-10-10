// SPDX-License-Identifier: Apache-2.0

package theme_test

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/johalputt/vayupress/internal/theme"
)

// migration108 runs the real migration file one statement per line, as the
// runner does, against a database holding only what the statement reads.
func migration108(t *testing.T, file string, seed ...string) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	for _, s := range append([]string{
		`CREATE TABLE theme_tokens(id INTEGER PRIMARY KEY CHECK(id=1),name TEXT NOT NULL DEFAULT 'Default',tokens TEXT NOT NULL DEFAULT '{}',updated_at TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE users(id TEXT PRIMARY KEY)`,
		`CREATE TABLE articles(id TEXT PRIMARY KEY)`,
	}, seed...) {
		if _, err := d.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile("../db/migrations/" + file)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range strings.Split(string(b), "\n") {
		if stmt = strings.TrimSpace(stmt); stmt != "" {
			if _, err := d.Exec(stmt); err != nil {
				t.Fatalf("%s: %v", file, err)
			}
		}
	}
	return d
}

func load(t *testing.T, d *sql.DB) theme.Tokens {
	t.Helper()
	tok, err := theme.Load(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// An install that existed before Halcyon must go on showing exactly what it
// showed: Default() as theme.Load returned it for a missing row, with none of
// the store's options added.
func TestMigration108PinsDefaultExactly(t *testing.T) {
	for name, seed := range map[string]string{
		"an install with a user": `INSERT INTO users(id) VALUES('u1')`,
		"an install with a post": `INSERT INTO articles(id) VALUES('a1')`,
	} {
		got := load(t, migration108(t, "108-theme-pin-existing.up.sql", seed))
		if !reflect.DeepEqual(got, theme.Default()) {
			t.Errorf("%s: pinned theme differs from Default():\n got %+v\nwant %+v", name, got, theme.Default())
		}
	}
}

// A database the migration finds empty is a new install: nothing is pinned,
// and it starts on Halcyon.
func TestMigration108LeavesNewInstallOnHalcyon(t *testing.T) {
	if got := load(t, migration108(t, "108-theme-pin-existing.up.sql")); got.Name != "Halcyon" {
		t.Errorf("a new install should start on Halcyon, got %q", got.Name)
	}
}

// A theme the operator saved is never replaced.
func TestMigration108KeepsASavedTheme(t *testing.T) {
	d := migration108(t, "108-theme-pin-existing.up.sql",
		`INSERT INTO users(id) VALUES('u1')`,
		`INSERT INTO theme_tokens(id,name,tokens) VALUES(1,'Orbit','{"Name":"Orbit"}')`)
	if got := load(t, d); got.Name != "Orbit" {
		t.Errorf("a saved theme was replaced: got %q", got.Name)
	}
}

// The down migration removes the pin only while it is untouched.
func TestMigration108DownRemovesOnlyTheUntouchedPin(t *testing.T) {
	up, err := os.ReadFile("../db/migrations/108-theme-pin-existing.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	var insert string
	for _, l := range strings.Split(string(up), "\n") {
		if strings.HasPrefix(l, "INSERT") {
			insert = l
		}
	}
	pinned := migration108(t, "108-theme-pin-existing.down.sql", `INSERT INTO users(id) VALUES('u1')`, insert)
	var n int
	if err := pinned.QueryRow(`SELECT COUNT(*) FROM theme_tokens`).Scan(&n); err != nil || n != 0 {
		t.Errorf("down left the untouched pin in place (rows %d, err %v)", n, err)
	}
	chosen := migration108(t, "108-theme-pin-existing.down.sql",
		`INSERT INTO theme_tokens(id,name,tokens) VALUES(1,'Default','{"Name":"Default","AccentLight":"#123456"}')`)
	if err := chosen.QueryRow(`SELECT COUNT(*) FROM theme_tokens`).Scan(&n); err != nil || n != 1 {
		t.Errorf("down removed a theme the operator had changed (rows %d, err %v)", n, err)
	}
}
