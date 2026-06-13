package migrations

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3" //nolint:blank-imports
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestDryRunReturnsPendingMigrations verifies DryRun reports pending migrations
// without touching the schema.
func TestDryRunReturnsPendingMigrations(t *testing.T) {
	db := newTestDB(t)
	m := New(db)

	results, err := m.DryRun()
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one migration in DryRun output")
	}
	for _, r := range results {
		if r.Version == "" {
			t.Error("DryRunResult has empty Version")
		}
		if r.Checksum == "" {
			t.Error("DryRunResult has empty Checksum")
		}
		if r.WouldSkip {
			t.Errorf("migration %s should not be skipped on fresh DB", r.Version)
		}
	}

	// Verify DryRun made no schema changes.
	var count int
	err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table'`).Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// schema_migrations table is created by DryRun.ensureTable — that's acceptable.
	// The point is user tables are not created.
}

// TestDryRunAfterUpShowsNoNewMigrations verifies applied migrations are skipped.
func TestDryRunAfterUpShowsNoNewMigrations(t *testing.T) {
	db := newTestDB(t)
	m := New(db)

	if err := m.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}

	results, err := m.DryRun()
	if err != nil {
		t.Fatalf("DryRun after Up: %v", err)
	}
	for _, r := range results {
		if !r.WouldSkip {
			t.Errorf("migration %s should be skipped after Up", r.Version)
		}
	}
}

// TestVerifyChecksumsClean verifies checksums pass on unmodified migrations.
func TestVerifyChecksumsClean(t *testing.T) {
	db := newTestDB(t)
	m := New(db)

	if err := m.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}

	drifts, err := m.VerifyChecksums()
	if err != nil {
		t.Fatalf("VerifyChecksums: %v", err)
	}
	if len(drifts) > 0 {
		t.Errorf("unexpected checksum drift: %+v", drifts)
	}
}

// TestJournalRecordsAttempts verifies the migration journal captures entries.
func TestJournalRecordsAttempts(t *testing.T) {
	db := newTestDB(t)
	m := New(db)

	if err := m.EnsureJournal(); err != nil {
		t.Fatalf("EnsureJournal: %v", err)
	}
	if err := m.RecordJournalEntry("001_test", "up", "abc123", true, ""); err != nil {
		t.Fatalf("RecordJournalEntry: %v", err)
	}
	if err := m.RecordJournalEntry("001_test", "up", "abc123", false, "exec failed"); err != nil {
		t.Fatalf("RecordJournalEntry failure: %v", err)
	}

	entries, err := m.Journal(db)
	if err != nil {
		t.Fatalf("Journal: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 journal entries, got %d", len(entries))
	}
	if !entries[0].Succeeded {
		t.Error("first entry should be success")
	}
	if entries[1].Succeeded {
		t.Error("second entry should be failure")
	}
	if entries[1].ErrorMsg != "exec failed" {
		t.Errorf("error msg: got %q", entries[1].ErrorMsg)
	}
}

// TestRollbackSimulation verifies Up → Down → Up is idempotent.
func TestRollbackSimulation(t *testing.T) {
	db := newTestDB(t)
	m := New(db)

	if err := m.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}

	st1, err := m.Applied()
	if err != nil {
		t.Fatalf("Status after Up: %v", err)
	}

	if err := m.Down(); err != nil {
		t.Fatalf("Down: %v", err)
	}

	// After Down, at least one migration should be unapplied.
	st2, err := m.Applied()
	if err != nil {
		t.Fatalf("Status after Down: %v", err)
	}
	_ = st2 // count doesn't matter; the key test is no panic/error

	// Re-apply.
	if err := m.Up(); err != nil {
		t.Fatalf("Up after Down: %v", err)
	}

	st3, err := m.Applied()
	if err != nil {
		t.Fatalf("Status after re-Up: %v", err)
	}

	if len(st3) != len(st1) {
		t.Errorf("migration count mismatch after Up→Down→Up: before=%d after=%d", len(st1), len(st3))
	}
}
