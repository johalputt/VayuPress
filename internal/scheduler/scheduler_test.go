package scheduler

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE scheduled_posts(id TEXT PRIMARY KEY,slug TEXT NOT NULL,title TEXT NOT NULL,content TEXT NOT NULL,tags TEXT NOT NULL DEFAULT '',publish_at DATETIME NOT NULL,status TEXT NOT NULL DEFAULT 'scheduled',error TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,published_at DATETIME)`)
	if err != nil {
		t.Fatal(err)
	}
	return New(db)
}

func TestScheduleAndDue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// One due in the past, one due in the future.
	if _, err := s.Schedule(ctx, "past", "Past", "body", []string{"a", "b"}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Schedule(ctx, "future", "Future", "body", nil, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	due, err := s.Due(ctx, time.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due post, got %d", len(due))
	}
	if due[0].Slug != "past" {
		t.Errorf("wrong due post: %s", due[0].Slug)
	}
	if len(due[0].Tags) != 2 {
		t.Errorf("tags not round-tripped: %v", due[0].Tags)
	}
}

func TestValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Schedule(ctx, "", "title", "body", nil, time.Now()); err == nil {
		t.Error("expected error for empty slug")
	}
	if _, err := s.Schedule(ctx, "slug", "title", "", nil, time.Now()); err == nil {
		t.Error("expected error for empty content")
	}
}

func TestMarkPublishedRemovesFromDue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p, _ := s.Schedule(ctx, "x", "X", "body", nil, time.Now().Add(-time.Minute))
	if err := s.MarkPublished(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	due, _ := s.Due(ctx, time.Now(), 10)
	if len(due) != 0 {
		t.Errorf("published post should not be due, got %d", len(due))
	}
}

func TestCancel(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p, _ := s.Schedule(ctx, "x", "X", "body", nil, time.Now().Add(time.Hour))
	if err := s.Cancel(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(ctx, p.ID); err == nil {
		t.Error("second cancel should fail (no longer scheduled)")
	}
	n, _ := s.PendingCount(ctx)
	if n != 0 {
		t.Errorf("pending count = %d, want 0", n)
	}
}
