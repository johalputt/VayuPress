package federation

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newReplayDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestReplayStoreNewIDNotSeen(t *testing.T) {
	db := newReplayDB(t)
	rs := NewReplayStore(db, time.Hour)
	if err := rs.EnsureSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}
	seen, err := rs.Seen("https://example.test/activity/1")
	if err != nil {
		t.Fatalf("Seen: %v", err)
	}
	if seen {
		t.Error("new activity ID should not be seen")
	}
}

func TestReplayStoreMarkAndSeen(t *testing.T) {
	db := newReplayDB(t)
	rs := NewReplayStore(db, time.Hour)
	if err := rs.EnsureSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}
	id := "https://example.test/activity/2"
	if err := rs.Mark(id); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	seen, err := rs.Seen(id)
	if err != nil {
		t.Fatalf("Seen: %v", err)
	}
	if !seen {
		t.Error("marked activity should be seen")
	}
}

func TestReplayStoreMarkOrRejectDuplicate(t *testing.T) {
	db := newReplayDB(t)
	rs := NewReplayStore(db, time.Hour)
	if err := rs.EnsureSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}
	id := "https://example.test/activity/3"
	if err := rs.MarkOrReject(id); err != nil {
		t.Fatalf("first MarkOrReject: %v", err)
	}
	if err := rs.MarkOrReject(id); err != ErrReplay {
		t.Errorf("duplicate MarkOrReject: got %v, want ErrReplay", err)
	}
}

func TestReplayStoreEmptyIDRejected(t *testing.T) {
	db := newReplayDB(t)
	rs := NewReplayStore(db, time.Hour)
	if err := rs.EnsureSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := rs.Mark(""); err == nil {
		t.Error("empty activity ID should be rejected")
	}
}

func TestReplayStoreCount(t *testing.T) {
	db := newReplayDB(t)
	rs := NewReplayStore(db, time.Hour)
	if err := rs.EnsureSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for i := 0; i < 5; i++ {
		rs.Mark(fmt.Sprintf("https://example.test/activity/%d", i)) //nolint:errcheck
	}
	n, err := rs.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 5 {
		t.Errorf("Count = %d, want 5", n)
	}
}

func TestReplayStoreSurvivesReopen(t *testing.T) {
	// Use a file DB to verify persistence across connections.
	tmp := t.TempDir()
	path := tmp + "/replay.db"

	write := func() {
		db, err := sql.Open("sqlite3", path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer db.Close()
		rs := NewReplayStore(db, time.Hour)
		if err := rs.EnsureSchema(); err != nil {
			t.Fatalf("schema: %v", err)
		}
		if err := rs.Mark("https://example.test/durable/1"); err != nil {
			t.Fatalf("Mark: %v", err)
		}
	}

	read := func() {
		db, err := sql.Open("sqlite3", path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer db.Close()
		rs := NewReplayStore(db, time.Hour)
		if err := rs.EnsureSchema(); err != nil {
			t.Fatalf("schema: %v", err)
		}
		seen, err := rs.Seen("https://example.test/durable/1")
		if err != nil {
			t.Fatalf("Seen: %v", err)
		}
		if !seen {
			t.Error("durable replay entry not found after reopen")
		}
	}

	write()
	read()
}
