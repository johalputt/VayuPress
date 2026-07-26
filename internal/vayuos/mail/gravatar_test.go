// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"crypto/md5" //nolint:gosec // spec identifier hash, matches production
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newAvatarStore(t *testing.T) *AccountStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatalf("NewAccountStore: %v", err)
	}
	return s
}

// pngBlob is a minimal valid PNG (magic bytes are enough for the store, which
// stores the MIME it is told).
var pngBlob = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}

// TestEmailAvatarHashes verifies the md5/sha256 digests match the Gravatar/
// Libravatar spec: lowercased, trimmed address.
func TestEmailAvatarHashes(t *testing.T) {
	md5hex, sha256hex := EmailAvatarHashes("  Ankush@Johal.IN ")
	wantMD5 := md5.Sum([]byte("ankush@johal.in")) //nolint:gosec
	wantSHA := sha256.Sum256([]byte("ankush@johal.in"))
	if md5hex != hex.EncodeToString(wantMD5[:]) {
		t.Errorf("md5 = %s, want normalized-address md5", md5hex)
	}
	if sha256hex != hex.EncodeToString(wantSHA[:]) {
		t.Errorf("sha256 = %s, want normalized-address sha256", sha256hex)
	}
}

// TestAvatarByHash verifies a stored picture is retrievable by BOTH its md5 and
// sha256 hash, that a mailbox without a picture never matches, and that an
// unknown hash errors.
func TestAvatarByHash(t *testing.T) {
	s := newAvatarStore(t)
	ctx := context.Background()

	if err := s.Create(ctx, "ankush@johal.in", "pw-1234567", "Ankush", RoleAdministrator); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Create(ctx, "plain@johal.in", "pw-1234567", "Plain", RoleMailbox); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.SetAvatar(ctx, "ankush@johal.in", pngBlob, "image/png"); err != nil {
		t.Fatalf("set avatar: %v", err)
	}

	md5hex, sha256hex := EmailAvatarHashes("ankush@johal.in")

	for _, h := range []string{md5hex, sha256hex} {
		blob, mime, err := s.AvatarByHash(ctx, h)
		if err != nil {
			t.Fatalf("AvatarByHash(%s) err: %v", h, err)
		}
		if mime != "image/png" || len(blob) == 0 {
			t.Errorf("AvatarByHash(%s) = (%d bytes, %q), want the png", h, len(blob), mime)
		}
	}

	// A mailbox with no picture must not resolve by its hash.
	pmd5, _ := EmailAvatarHashes("plain@johal.in")
	if _, _, err := s.AvatarByHash(ctx, pmd5); err == nil {
		t.Error("a mailbox without a picture must not resolve by hash")
	}
	// An unknown hash errors.
	if _, _, err := s.AvatarByHash(ctx, "deadbeefdeadbeefdeadbeefdeadbeef"); err == nil {
		t.Error("an unknown hash should error")
	}
}
