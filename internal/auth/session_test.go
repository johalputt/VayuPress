package auth

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newSessionStore(t *testing.T) *SessionStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE sessions(token_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,expires_at DATETIME NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	return NewSessionStore(db)
}

// TestSessionRoundTrip guards against the DATETIME-format regression where a
// freshly issued session was read back as already expired.
func TestSessionRoundTrip(t *testing.T) {
	s := newSessionStore(t)
	ctx := context.Background()
	token, err := s.Create(ctx, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	uid, err := s.Validate(ctx, token)
	if err != nil {
		t.Fatalf("freshly created session should be valid, got: %v", err)
	}
	if uid != "user-1" {
		t.Errorf("uid = %q, want user-1", uid)
	}
	if err := s.Destroy(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Validate(ctx, token); err == nil {
		t.Error("destroyed session should not validate")
	}
}
