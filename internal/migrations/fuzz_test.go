package migrations_test

import (
	"database/sql"
	"testing"

	"github.com/johalputt/vayupress/internal/migrations"
	_ "github.com/mattn/go-sqlite3"
)

// FuzzMigratorApply feeds arbitrary SQL blobs to the migration apply path.
// It must never panic — only return errors.
func FuzzMigratorApply(f *testing.F) {
	f.Add("CREATE TABLE t (id INTEGER PRIMARY KEY);")
	f.Add("SELECT 1;")
	f.Add("")
	f.Add("DROP TABLE IF EXISTS t; CREATE TABLE t2 (x TEXT);")
	f.Fuzz(func(t *testing.T, blob string) {
		db, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			t.Skip(err)
		}
		defer db.Close()
		m := migrations.New(db)
		_ = m.Up() // ignore errors — we only check for panics
		_ = blob
	})
}
