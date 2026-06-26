package secrets

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
	t.Cleanup(func() { db.Close() })
	schema := `CREATE TABLE service_credentials(id TEXT PRIMARY KEY,provider TEXT NOT NULL,label TEXT NOT NULL DEFAULT '',endpoint TEXT NOT NULL DEFAULT '',secret_nonce TEXT NOT NULL DEFAULT '',secret_ct TEXT NOT NULL DEFAULT '',hint TEXT NOT NULL DEFAULT '',enabled INTEGER NOT NULL DEFAULT 1,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return New(db, []byte("test-master-secret"))
}

func TestUpsertSealsAndReveals(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id, err := s.Upsert(ctx, ProviderIndexNow, "IndexNow", "", "super-secret-key-1234", true, false)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Ciphertext must not contain the plaintext.
	var ct string
	if err := s.db.QueryRow(`SELECT secret_ct FROM service_credentials WHERE id=?`, id).Scan(&ct); err != nil {
		t.Fatalf("query ct: %v", err)
	}
	if strings.Contains(ct, "super-secret") {
		t.Fatal("plaintext leaked into stored ciphertext")
	}
	got, err := s.Reveal(ctx, id)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if got != "super-secret-key-1234" {
		t.Fatalf("reveal = %q", got)
	}
}

func TestProviderSecretRespectsEnabled(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Upsert(ctx, ProviderIndexNow, "IndexNow", "", "abc123", true, false); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	key, _ := s.ProviderSecret(ctx, ProviderIndexNow)
	if key != "abc123" {
		t.Fatalf("ProviderSecret = %q", key)
	}
	// Disable it; ProviderSecret should now return empty.
	id, _ := s.Upsert(ctx, ProviderIndexNow, "IndexNow", "", "", false, false)
	_ = id
	if key, _ := s.ProviderSecret(ctx, ProviderIndexNow); key != "" {
		t.Fatalf("disabled credential should not be returned, got %q", key)
	}
}

func TestUpsertPreservesSecretWhenBlank(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id, err := s.Upsert(ctx, ProviderN8N, "n8n automation", "https://n8n.example.com/webhook/x", "token-xyz", true, false)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Update endpoint only, leaving secret blank — the stored secret must remain.
	if _, err := s.Upsert(ctx, ProviderN8N, "n8n automation", "https://n8n.example.com/webhook/y", "", true, false); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.Reveal(ctx, id)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if got != "token-xyz" {
		t.Fatalf("secret should be preserved, got %q", got)
	}
	_, ep := s.ProviderSecret(ctx, ProviderN8N)
	if ep != "https://n8n.example.com/webhook/y" {
		t.Fatalf("endpoint not updated, got %q", ep)
	}
}

func TestMaskHidesSecret(t *testing.T) {
	if h := mask("abcdefghijkl"); !strings.HasSuffix(h, "ijkl") || strings.Contains(h, "abcd") {
		t.Fatalf("mask leaked too much: %q", h)
	}
	if mask("") != "" {
		t.Fatal("empty secret should mask to empty")
	}
}

func TestUnknownProviderRejected(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Upsert(context.Background(), "definitely-not-real", "x", "", "y", true, false); err == nil {
		t.Fatal("expected unknown provider to be rejected")
	}
}

func TestListReportsMetadata(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Upsert(ctx, ProviderOpenRouter, "OpenRouter", "https://openrouter.ai/api/v1", "sk-or-test", true, false); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(list))
	}
	c := list[0]
	if !c.HasSecret || !c.Enabled || c.Hint == "" {
		t.Fatalf("unexpected metadata: %+v", c)
	}
	if strings.Contains(c.Hint, "sk-or-test") {
		t.Fatalf("hint must be masked, got %q", c.Hint)
	}
}
