package auth

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestSessionValidateUsesReaderReadYourWrites proves the writer/reader split is
// safe: a session created on the writer connection validates immediately through
// a SEPARATE reader connection (as in production, where UseReader points Validate
// at the read pool). This guards the admin-only-502 fix — moving the per-request
// auth read off the single writer must not break read-your-writes.
func TestSessionValidateUsesReaderReadYourWrites(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "sess.db") + "?_journal_mode=WAL&_busy_timeout=5000"

	writer, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Exec(`CREATE TABLE sessions(token_hash TEXT PRIMARY KEY, user_id TEXT NOT NULL, expires_at DATETIME NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	reader, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	s := NewSessionStore(writer)
	s.UseReader(reader)

	// Create on the writer...
	token, err := s.Create(context.Background(), "user-42")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// ...and validate through the distinct reader connection.
	uid, err := s.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("validate via reader failed (read-your-writes broken): %v", err)
	}
	if uid != "user-42" {
		t.Fatalf("uid = %q, want user-42", uid)
	}

	// An unknown token must still be rejected.
	if _, err := s.Validate(context.Background(), "deadbeef"); err == nil {
		t.Error("unknown token should be invalid")
	}
}
