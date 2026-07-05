package search

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TestIndexPersistenceRoundTripAndDelta verifies SaveIndex/LoadIndex restore the
// index and reconcile only what changed (add/update/delete) without a full rescan.
func TestIndexPersistenceRoundTripAndDelta(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE articles(
		id TEXT PRIMARY KEY, title TEXT, slug TEXT, content TEXT, tags TEXT,
		status TEXT, is_page INTEGER, created_at DATETIME, updated_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	ins := func(id, title, content string, up time.Time) {
		if _, e := db.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,is_page,created_at,updated_at)
			VALUES(?,?,?,?,?, 'published',0,?,?)`, id, title, "slug-"+id, content, "", up, up); e != nil {
			t.Fatal(e)
		}
	}
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	ins("a", "Alpha", "hello alpha", t0)
	ins("b", "Beta", "hello beta", t0)

	s := NewService(db).(*builtinService)
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.DocCount(context.Background()); n != 2 {
		t.Fatalf("initial docs=%d want 2", n)
	}
	path := filepath.Join(t.TempDir(), "idx.gob")
	if err := s.SaveIndex(context.Background(), path); err != nil {
		t.Fatal(err)
	}

	// Mutate: update a, add c, delete b.
	t1 := time.Now().Truncate(time.Second)
	if _, e := db.Exec(`UPDATE articles SET title='Alpha2', content='updated', updated_at=? WHERE id='a'`, t1); e != nil {
		t.Fatal(e)
	}
	ins("c", "Gamma", "hello gamma", t1)
	if _, e := db.Exec(`DELETE FROM articles WHERE id='b'`); e != nil {
		t.Fatal(e)
	}

	// Fresh service restores from snapshot and reconciles deltas.
	s2 := NewService(db).(*builtinService)
	ok, err := s2.LoadIndex(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("LoadIndex returned false; expected a snapshot restore")
	}
	if n, _ := s2.DocCount(context.Background()); n != 2 {
		t.Fatalf("post-delta docs=%d want 2 (a,c)", n)
	}
	s2.mu.RLock()
	da, dc := s2.byID["a"], s2.byID["c"]
	_, bExists := s2.byID["b"]
	s2.mu.RUnlock()
	if da == nil || da.Title != "Alpha2" {
		t.Errorf("a not updated from snapshot: %+v", da)
	}
	if bExists {
		t.Error("b should have been removed (deleted upstream)")
	}
	if dc == nil || dc.Title != "Gamma" {
		t.Errorf("c not added by reconcile: %+v", dc)
	}

	// Missing snapshot → false so the caller falls back to a full Load.
	s3 := NewService(db).(*builtinService)
	if ok, _ := s3.LoadIndex(context.Background(), filepath.Join(t.TempDir(), "absent.gob")); ok {
		t.Error("LoadIndex on a missing file should return false")
	}
}
