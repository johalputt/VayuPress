package db

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestAllMigrationsApplyCleanly runs every embedded migration against a fresh
// in-memory database through the real runner. The runner executes one statement
// per line, so a multi-line statement fails here with "incomplete input" — this
// guards that convention and would have caught the broken 037 migration before
// it reached main (where it broke fresh installs + the server-start CI jobs).
func TestAllMigrationsApplyCleanly(t *testing.T) {
	d, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// :memory: is per-connection — pin the pool so migrations and the assertions
	// below share the same database.
	d.SetMaxOpenConns(1)
	defer d.Close()

	old := DB
	DB = d
	defer func() { DB = old }()

	if err := runMigrations(); err != nil {
		t.Fatalf("runMigrations on a fresh DB failed: %v", err)
	}

	// Sanity-check a table + seed row from the migration that was previously
	// broken, proving it now applies.
	var tiers int
	if err := d.QueryRow(`SELECT COUNT(*) FROM member_tiers`).Scan(&tiers); err != nil {
		t.Fatalf("member_tiers not created by migrations: %v", err)
	}
	if tiers < 2 {
		t.Errorf("expected the seeded free + paid tiers, got %d", tiers)
	}
}
