// SPDX-License-Identifier: Apache-2.0

package federation

// Audit H2 made inbox signature verification mandatory, so these tests deliver
// signed activities via the same signedRequest/pubPEM helpers used for the
// signature-verification tests (the package switched from federation_test to
// federation to reach them).

import (
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var errUnknownTestKey = errors.New("unknown test key")

// testPeerKey generates a fresh 2048-bit peer key for signing deliveries.
func testPeerKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate peer key: %v", err)
	}
	return priv
}

func TestPublishAndOutbox(t *testing.T) {
	s := NewServer("https://example.com", "alice", "Alice")
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

// newInboxTestServer returns a server whose resolver knows exactly one peer
// key, plus that key. Every delivery must be signed with it.
func newInboxTestServer(t *testing.T, keyID string) (*Server, func(Activity) int) {
	t.Helper()
	priv := testPeerKey(t)
	s := NewServer("https://example.com", "alice", "Alice")
	s.SetKeyResolver(func(id string) (string, error) {
		if id == keyID {
			return pubPEM(t, priv), nil
		}
		return "", errUnknownTestKey
	})
	post := func(act Activity) int {
		body, _ := json.Marshal(act)
		req := signedRequest(t, priv, keyID, body, time.Now())
		rec := httptest.NewRecorder()
		s.InboxHandler(rec, req)
		return rec.Code
	}
	return s, post
}

func TestInboxFollow(t *testing.T) {
	keyID := "https://mastodon.social/users/bob#key"
	s, post := newInboxTestServer(t, keyID)
	code := post(Activity{Type: ActivityFollow, Actor: "https://mastodon.social/users/bob"})
	if code != 202 {
		t.Errorf("inbox POST: got %d", code)
	}
	if len(s.Followers()) != 1 {
		t.Errorf("expected 1 follower, got %d", len(s.Followers()))
	}
}

func TestInboxReplayProtection(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	rs := NewReplayStore(db, 0)
	if err := rs.EnsureSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}

	keyID := "https://peer/users/bob#key"
	s, post := newInboxTestServer(t, keyID)
	s.SetReplayStore(rs)

	act := Activity{ID: "https://peer/activities/1", Type: ActivityCreate, Actor: "https://peer/users/bob"}

	// First delivery is processed.
	if code := post(act); code != 202 {
		t.Fatalf("first delivery: got %d, want 202", code)
	}
	if s.InboxCount() != 1 {
		t.Fatalf("after first delivery inbox=%d, want 1", s.InboxCount())
	}

	// Re-delivery of the same activity id is accepted idempotently (200) and is
	// NOT processed again.
	if code := post(act); code != 200 {
		t.Fatalf("replay delivery: got %d, want 200 (idempotent)", code)
	}
	if s.InboxCount() != 1 {
		t.Errorf("replay was processed: inbox=%d, want still 1", s.InboxCount())
	}

	// A different id is fresh and processed.
	act2 := act
	act2.ID = "https://peer/activities/2"
	if code := post(act2); code != 202 {
		t.Fatalf("second distinct delivery: got %d, want 202", code)
	}
	if s.InboxCount() != 2 {
		t.Errorf("distinct activity not processed: inbox=%d, want 2", s.InboxCount())
	}
}

func TestInboxReplayRejectsMissingID(t *testing.T) {
	db, _ := sql.Open("sqlite3", ":memory:")
	t.Cleanup(func() { db.Close() })
	rs := NewReplayStore(db, 0)
	if err := rs.EnsureSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}
	keyID := "https://peer/u/x#key"
	s, post := newInboxTestServer(t, keyID)
	s.SetReplayStore(rs)

	// With replay protection on, an activity that carries no id cannot be
	// deduplicated and must be refused rather than silently admitted.
	if code := post(Activity{Type: ActivityCreate, Actor: "https://peer/u/x"}); code != 400 {
		t.Errorf("missing-id activity: got %d, want 400", code)
	}
	if s.InboxCount() != 0 {
		t.Errorf("id-less activity was processed: inbox=%d, want 0", s.InboxCount())
	}
}
