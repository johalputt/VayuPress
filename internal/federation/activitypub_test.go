package federation_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/federation"
	_ "github.com/mattn/go-sqlite3"
)

func TestPublishAndOutbox(t *testing.T) {
	s := federation.NewServer("https://example.com", "alice", "Alice")
	s.Publish("post-1", "Note", "Hello federation!")
	if s.InboxCount() != 0 {
		t.Error("inbox should be empty")
	}
	req := httptest.NewRequest("GET", "/outbox", nil)
	rec := httptest.NewRecorder()
	s.OutboxHandler(rec, req)
	if rec.Code != 200 {
		t.Errorf("outbox: got %d", rec.Code)
	}
}

func TestInboxFollow(t *testing.T) {
	s := federation.NewServer("https://example.com", "alice", "Alice")
	act := federation.Activity{
		Type:  federation.ActivityFollow,
		Actor: "https://mastodon.social/users/bob",
	}
	body, _ := json.Marshal(act)
	req := httptest.NewRequest("POST", "/inbox", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.InboxHandler(rec, req)
	if rec.Code != 202 {
		t.Errorf("inbox POST: got %d", rec.Code)
	}
	if len(s.Followers()) != 1 {
		t.Errorf("expected 1 follower, got %d", len(s.Followers()))
	}
}

// post is a small helper that delivers an activity to the inbox and returns the
// HTTP status code.
func post(t *testing.T, s *federation.Server, act federation.Activity) int {
	t.Helper()
	body, _ := json.Marshal(act)
	req := httptest.NewRequest("POST", "/inbox", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.InboxHandler(rec, req)
	return rec.Code
}

func TestInboxReplayProtection(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	rs := federation.NewReplayStore(db, 0)
	if err := rs.EnsureSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}

	s := federation.NewServer("https://example.com", "alice", "Alice")
	s.SetReplayStore(rs)

	act := federation.Activity{ID: "https://peer/activities/1", Type: federation.ActivityCreate, Actor: "https://peer/users/bob"}

	// First delivery is processed.
	if code := post(t, s, act); code != 202 {
		t.Fatalf("first delivery: got %d, want 202", code)
	}
	if s.InboxCount() != 1 {
		t.Fatalf("after first delivery inbox=%d, want 1", s.InboxCount())
	}

	// Re-delivery of the same activity id is accepted idempotently (200) and is
	// NOT processed again.
	if code := post(t, s, act); code != 200 {
		t.Fatalf("replay delivery: got %d, want 200 (idempotent)", code)
	}
	if s.InboxCount() != 1 {
		t.Errorf("replay was processed: inbox=%d, want still 1", s.InboxCount())
	}

	// A different id is fresh and processed.
	act2 := act
	act2.ID = "https://peer/activities/2"
	if code := post(t, s, act2); code != 202 {
		t.Fatalf("second distinct delivery: got %d, want 202", code)
	}
	if s.InboxCount() != 2 {
		t.Errorf("distinct activity not processed: inbox=%d, want 2", s.InboxCount())
	}
}

func TestInboxReplayRejectsMissingID(t *testing.T) {
	db, _ := sql.Open("sqlite3", ":memory:")
	t.Cleanup(func() { db.Close() })
	rs := federation.NewReplayStore(db, 0)
	if err := rs.EnsureSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}
	s := federation.NewServer("https://example.com", "alice", "Alice")
	s.SetReplayStore(rs)

	// With replay protection on, an activity that carries no id cannot be
	// deduplicated and must be refused rather than silently admitted.
	if code := post(t, s, federation.Activity{Type: federation.ActivityCreate, Actor: "https://peer/u/x"}); code != 400 {
		t.Errorf("missing-id activity: got %d, want 400", code)
	}
	if s.InboxCount() != 0 {
		t.Errorf("id-less activity was processed: inbox=%d, want 0", s.InboxCount())
	}
}
