package newsletter

import (
	"bytes"
	"context"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"database/sql"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE newsletter_subscribers(id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE,status TEXT NOT NULL DEFAULT 'active',confirmed INTEGER NOT NULL DEFAULT 0,token TEXT NOT NULL DEFAULT '',subscribed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,unsubscribed_at DATETIME)`,
		`CREATE TABLE newsletter_broadcasts(id TEXT PRIMARY KEY,subject TEXT NOT NULL,recipients INTEGER NOT NULL DEFAULT 0,sent INTEGER NOT NULL DEFAULT 0,failed INTEGER NOT NULL DEFAULT 0,status TEXT NOT NULL DEFAULT 'sending',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,completed_at DATETIME)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return New(db)
}

// confirm is a tiny helper that subscribes then confirms an address.
func confirmSub(t *testing.T, s *Store, email string) *Subscriber {
	t.Helper()
	sub, _, err := s.Subscribe(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Confirm(context.Background(), sub.Token); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestStatsSegments(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	confirmSub(t, s, "active1@x.com")
	confirmSub(t, s, "active2@x.com")
	// pending (subscribed, not confirmed)
	if _, _, err := s.Subscribe(ctx, "pending@x.com"); err != nil {
		t.Fatal(err)
	}
	// unsubscribed
	gone := confirmSub(t, s, "gone@x.com")
	if err := s.Unsubscribe(ctx, gone.Token); err != nil {
		t.Fatal(err)
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Total != 4 {
		t.Errorf("Total = %d, want 4", st.Total)
	}
	if st.Active != 2 {
		t.Errorf("Active = %d, want 2", st.Active)
	}
	if st.Pending != 1 {
		t.Errorf("Pending = %d, want 1", st.Pending)
	}
	if st.Unsubscribed != 1 {
		t.Errorf("Unsubscribed = %d, want 1", st.Unsubscribed)
	}
	if st.NewLast30 != 4 {
		t.Errorf("NewLast30 = %d, want 4", st.NewLast30)
	}
	// confirm rate = active / (active+pending) = 2/3
	if st.ConfirmRate < 0.66 || st.ConfirmRate > 0.67 {
		t.Errorf("ConfirmRate = %.3f, want ~0.667", st.ConfirmRate)
	}
}

func TestListFilterAndSearch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	confirmSub(t, s, "alice@example.com")
	if _, _, err := s.Subscribe(ctx, "bob@pending.com"); err != nil { // pending
		t.Fatal(err)
	}

	active, _ := s.List(ctx, "active", "", 100)
	if len(active) != 1 || active[0].Email != "alice@example.com" {
		t.Fatalf("active filter wrong: %+v", active)
	}
	pending, _ := s.List(ctx, "pending", "", 100)
	if len(pending) != 1 || pending[0].Email != "bob@pending.com" {
		t.Fatalf("pending filter wrong: %+v", pending)
	}
	all, _ := s.List(ctx, "all", "", 100)
	if len(all) != 2 {
		t.Fatalf("all should return 2, got %d", len(all))
	}
	hit, _ := s.List(ctx, "all", "ALICE", 100) // case-insensitive
	if len(hit) != 1 || hit[0].Email != "alice@example.com" {
		t.Fatalf("search wrong: %+v", hit)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sub := confirmSub(t, s, "delete@x.com")
	if err := s.Delete(ctx, sub.ID); err != nil {
		t.Fatal(err)
	}
	all, _ := s.List(ctx, "all", "", 100)
	if len(all) != 0 {
		t.Errorf("subscriber should be gone, got %d", len(all))
	}
}

func TestExportCSV(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	confirmSub(t, s, "csv@x.com")
	var buf bytes.Buffer
	if err := s.ExportCSV(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "email,status,confirmed,subscribed_at,unsubscribed_at") {
		t.Errorf("bad CSV header: %q", strings.SplitN(out, "\n", 2)[0])
	}
	if !strings.Contains(out, "csv@x.com") || !strings.Contains(out, "true") {
		t.Errorf("CSV missing subscriber data:\n%s", out)
	}
}

func TestGrowthByDay(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	confirmSub(t, s, "g1@x.com")
	confirmSub(t, s, "g2@x.com")
	series, err := s.GrowthByDay(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 30 {
		t.Fatalf("series length = %d, want 30", len(series))
	}
	// Both signups happened today (last bucket).
	if series[29] != 2 {
		t.Errorf("today's bucket = %d, want 2", series[29])
	}
}

func TestBroadcastHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id, err := s.CreateBroadcast(ctx, "Hello world", 10)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListBroadcasts(ctx, 10)
	if len(list) != 1 || list[0].Status != "sending" || list[0].Recipients != 10 {
		t.Fatalf("broadcast not recorded correctly: %+v", list)
	}
	if err := s.FinishBroadcast(ctx, id, 9, 1); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListBroadcasts(ctx, 10)
	if list[0].Status != "complete" || list[0].Sent != 9 || list[0].Failed != 1 || list[0].CompletedAt == nil {
		t.Fatalf("broadcast not finished correctly: %+v", list[0])
	}
}
