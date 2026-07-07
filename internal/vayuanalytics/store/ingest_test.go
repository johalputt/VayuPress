package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// fileStore opens a WAL file-backed store so the async ingest goroutine's
// separate connection sees the schema (a :memory: DB is per-connection).
func fileStore(t *testing.T) *Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "va.db") + "?_journal_mode=WAL&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return New(db)
}

// TestQueueBatchedIngestPersists proves the async batched path persists both an
// enter and its beacon in one flush, folding the beacon into the enter row —
// this is the fix that keeps beacon writes off the request path and out of the
// writer's synchronous critical section.
func TestQueueBatchedIngestPersists(t *testing.T) {
	s := fileStore(t)
	done := make(chan struct{})
	s.StartIngest(done)

	now := time.Now().UTC()
	s.QueueEnter(enter("s1", "/p", "organic", "Google", "human", now))
	s.QueueBeacon(BeaconInput{SessionHash: "s1", PagePath: "/p", TimeOnPage: 45, ScrollDepth: 60, Interactions: 3, Now: now.Add(time.Second)})

	// Force a prompt drain+flush and wait for it to land.
	close(done)
	deadline := time.Now().Add(2 * time.Second)
	var views int64
	for time.Now().Before(deadline) {
		ov, err := s.Overview(context.Background(), 30)
		if err == nil && ov.Views >= 1 {
			views = ov.Views
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if views != 1 {
		t.Fatalf("batched enter not persisted: views=%d", views)
	}
	// The beacon (45s / 60% scroll) should have folded into the row as engaged.
	var engaged, tos, scroll int
	if err := s.db.QueryRow(`SELECT engaged,time_on_page_seconds,scroll_depth_percent FROM vayuanalytics_sessions WHERE session_hash='s1'`).
		Scan(&engaged, &tos, &scroll); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if engaged != 1 || tos != 45 || scroll != 60 {
		t.Fatalf("beacon not folded: engaged=%d tos=%d scroll=%d", engaged, tos, scroll)
	}
	if s.Dropped() != 0 {
		t.Errorf("no events should have been dropped, got %d", s.Dropped())
	}
}

// TestQueueSynchronousFallback proves Queue* persist synchronously when
// StartIngest has not been called (tests, one-off callers).
func TestQueueSynchronousFallback(t *testing.T) {
	s := testStore(t) // in-memory, ingestion NOT started
	s.QueueEnter(enter("s2", "/x", "direct", "", "human", time.Now().UTC()))
	o, err := s.Overview(context.Background(), 30)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if o.Views != 1 {
		t.Fatalf("synchronous fallback did not persist: views=%d", o.Views)
	}
}
