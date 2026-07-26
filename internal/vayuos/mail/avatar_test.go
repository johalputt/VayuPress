// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestAccountAvatar pins the per-mailbox profile-picture storage: set → present
// in List() (via AvatarType) and fetchable, then cleared back to none.
func TestAccountAvatar(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ctx := context.Background()
	if err := s.Create(ctx, "alice@example.com", "h", "Alice", "author"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// No avatar initially.
	accs, _ := s.List(ctx)
	if len(accs) != 1 || accs[0].AvatarType != "" {
		t.Fatalf("new account should have no avatar: %+v", accs)
	}
	if blob, mime, _ := s.Avatar(ctx, "alice@example.com"); len(blob) != 0 || mime != "" {
		t.Fatalf("Avatar() should be empty, got %d bytes / %q", len(blob), mime)
	}

	// Store one.
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3}
	if err := s.SetAvatar(ctx, "Alice@Example.com", png, "image/png"); err != nil { // case-insensitive key
		t.Fatalf("set avatar: %v", err)
	}
	accs, _ = s.List(ctx)
	if accs[0].AvatarType != "image/png" {
		t.Fatalf("List should reflect avatar_type, got %q", accs[0].AvatarType)
	}
	blob, mime, err := s.Avatar(ctx, "alice@example.com")
	if err != nil || mime != "image/png" || !bytes.Equal(blob, png) {
		t.Fatalf("Avatar() mismatch: mime=%q err=%v equal=%v", mime, err, bytes.Equal(blob, png))
	}

	// Clear it.
	if err := s.ClearAvatar(ctx, "alice@example.com"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	accs, _ = s.List(ctx)
	if accs[0].AvatarType != "" {
		t.Fatalf("cleared account should have no avatar_type, got %q", accs[0].AvatarType)
	}
}
