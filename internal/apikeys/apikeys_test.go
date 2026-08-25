// SPDX-License-Identifier: Apache-2.0

package apikeys

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Pin to a single connection: each :memory: connection is a *separate*
	// database, so without this the pool can hand a query a fresh, empty DB and
	// make writes invisible (a flake that surfaces under -race timing).
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	// Schema mirrors migrations 041 + 042 + 062 + 092 (the columns fine-grained
	// permissions and domain-scoping add), so the store's queries exercise the
	// real shape.
	schema := `CREATE TABLE vayu_api_keys(id TEXT PRIMARY KEY,label TEXT NOT NULL DEFAULT '',prefix TEXT NOT NULL DEFAULT '',key_hash TEXT NOT NULL,scope TEXT NOT NULL DEFAULT 'external',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,last_used_at DATETIME,revoked INTEGER NOT NULL DEFAULT 0,permissions TEXT NOT NULL DEFAULT '{}',expires_at DATETIME,owner_user_id TEXT NOT NULL DEFAULT '',rate_per_min INTEGER NOT NULL DEFAULT 0,use_count INTEGER NOT NULL DEFAULT 0,active INTEGER NOT NULL DEFAULT 1,domain_id TEXT NOT NULL DEFAULT '')`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return New(db)
}

func TestCreateAndVerify(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	key, raw, err := s.CreateWithPermissions(ctx, "", "CI bot", Superuser(), nil, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(raw, TokenPrefix) {
		t.Errorf("raw token missing prefix: %q", raw)
	}
	if !strings.HasPrefix(key.Prefix, TokenPrefix) {
		t.Errorf("stored prefix missing scheme: %q", key.Prefix)
	}
	if key.Label != "CI bot" {
		t.Errorf("label = %q", key.Label)
	}
	if !s.Verify(raw) {
		t.Error("freshly created key should verify")
	}
	if s.Verify(raw + "x") {
		t.Error("tampered token must not verify")
	}
	if s.Verify("") {
		t.Error("empty token must not verify")
	}
}

func TestRotateInvalidatesOldToken(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	key, raw, err := s.CreateWithPermissions(ctx, "", "deploy", Superuser(), nil, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newRaw, err := s.Rotate(ctx, key.ID)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newRaw == raw {
		t.Fatal("rotate must produce a different token")
	}
	if s.Verify(raw) {
		t.Error("old token must stop verifying after rotation")
	}
	if !s.Verify(newRaw) {
		t.Error("new token must verify after rotation")
	}
}

func TestRevokeAndDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	key, raw, err := s.CreateWithPermissions(ctx, "", "temp", Superuser(), nil, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Revoke(ctx, key.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if s.Verify(raw) {
		t.Error("revoked token must not verify")
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || !list[0].Revoked {
		t.Fatalf("expected one revoked key, got %+v", list)
	}
	if err := s.Delete(ctx, key.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ = s.List(ctx)
	if len(list) != 0 {
		t.Fatalf("expected no keys after delete, got %d", len(list))
	}
}

func TestRotateUnknownReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Rotate(context.Background(), "does-not-exist"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestEnsureInternalProvisionsAndPropagates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.EnsureInternal(ctx); err != nil {
		t.Fatalf("ensure internal: %v", err)
	}
	raw := s.InternalKey()
	if raw == "" {
		t.Fatal("internal key should be available after EnsureInternal")
	}
	if !s.Verify(raw) {
		t.Error("internal key should authenticate")
	}
	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].Scope != ScopeInternal || list[0].ID != InternalKeyID {
		t.Fatalf("expected one internal key, got %+v", list)
	}
	// The internal/system key must NOT be rotatable through the store surface:
	// Rotate returns the fresh secret, so allowing it would mint an unconditional
	// superuser token (audit C2). It is refreshed only by EnsureInternal at boot.
	if _, err := s.Rotate(ctx, InternalKeyID); err != ErrInternalProtected {
		t.Fatalf("rotate internal: expected ErrInternalProtected, got %v", err)
	}
	if s.InternalKey() != raw || !s.Verify(raw) {
		t.Fatal("internal key must be unchanged after a refused rotate")
	}
	// EnsureInternal is the legitimate refresh path: it re-provisions the secret,
	// propagates the new value to InternalKey() live, and retires the old one.
	if err := s.EnsureInternal(ctx); err != nil {
		t.Fatalf("re-ensure internal: %v", err)
	}
	newRaw := s.InternalKey()
	if newRaw == raw {
		t.Fatal("EnsureInternal should refresh the internal secret")
	}
	if s.Verify(raw) {
		t.Error("old internal value must stop authenticating")
	}
	if !s.Verify(newRaw) {
		t.Error("new internal value must authenticate")
	}
}

func TestInternalKeyCannotBeRevokedOrDeleted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.EnsureInternal(ctx); err != nil {
		t.Fatalf("ensure internal: %v", err)
	}
	if err := s.Revoke(ctx, InternalKeyID); err != ErrInternalProtected {
		t.Fatalf("revoke internal: expected ErrInternalProtected, got %v", err)
	}
	if err := s.Delete(ctx, InternalKeyID); err != ErrInternalProtected {
		t.Fatalf("delete internal: expected ErrInternalProtected, got %v", err)
	}
}

func TestEnsureInternalIsIdempotentRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.EnsureInternal(ctx)
	_ = s.EnsureInternal(ctx)
	list, _ := s.List(ctx)
	count := 0
	for _, k := range list {
		if k.Scope == ScopeInternal {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one internal key row, got %d", count)
	}
}
