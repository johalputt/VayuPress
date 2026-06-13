package federation_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/federation"
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
