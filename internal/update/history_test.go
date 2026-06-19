package update

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestHistoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := New(testDB(t))

	id, err := st.Log(ctx, Record{FromVersion: "v1.0.0", ToVersion: "v1.1.0", Status: "started"})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if id == 0 {
		t.Fatal("expected nonzero id")
	}

	if err := st.MarkComplete(ctx, id, "success", "all good"); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}

	recs, err := st.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	r := recs[0]
	if r.Status != "success" || r.Detail != "all good" {
		t.Errorf("unexpected record: %+v", r)
	}
	if r.CompletedAt == nil {
		t.Error("expected completed_at set")
	}
	if r.FromVersion != "v1.0.0" || r.ToVersion != "v1.1.0" {
		t.Errorf("versions wrong: %+v", r)
	}
}
