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
	return New(newTestDB(t), nil)
}

func newTestDB(t *testing.T) *sql.DB {
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
	keyring := `CREATE TABLE secret_keyring(id INTEGER PRIMARY KEY,dek TEXT NOT NULL,kek_src TEXT NOT NULL DEFAULT 'none',kek_check TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,rotated_at DATETIME)`
	if _, err := db.Exec(keyring); err != nil {
		t.Fatalf("keyring schema: %v", err)
	}
	return db
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

// TestSecretsSurviveAcrossStores proves the DEK is persisted in the keyring, so
// a brand-new Store on the same DB (as happens on restart, or after the auth
// API key is rotated — which no longer touches encryption) can still decrypt.
func TestSecretsSurviveAcrossStores(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	a := New(db, nil)
	id, err := a.Upsert(ctx, ProviderIndexNow, "IndexNow", "", "key-survives-rotation", true, false)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Simulate a restart / API-key rotation: a fresh store, no shared state.
	b := New(db, nil)
	got, err := b.Reveal(ctx, id)
	if err != nil {
		t.Fatalf("reveal from new store: %v", err)
	}
	if got != "key-survives-rotation" {
		t.Fatalf("secret did not survive: %q", got)
	}
}

// TestEnvWrappedKeyringRoundTrip checks the VAYU_SECRET-wrapped path.
func TestEnvWrappedKeyringRoundTrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	a := New(db, []byte("vayu-secret-passphrase"))
	id, err := a.Upsert(ctx, ProviderOpenRouter, "OpenRouter", "", "sk-or-wrapped", true, false)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// New store with the same secret can decrypt.
	b := New(db, []byte("vayu-secret-passphrase"))
	if got, err := b.Reveal(ctx, id); err != nil || got != "sk-or-wrapped" {
		t.Fatalf("reveal with correct secret: got %q err %v", got, err)
	}
	// A store with the WRONG secret must fail to initialise the keyring.
	c := New(db, []byte("wrong-secret"))
	if _, err := c.Reveal(ctx, id); err == nil {
		t.Fatal("expected failure decrypting with the wrong VAYU_SECRET")
	}
	// A store with NO secret must also fail (the keyring is env-wrapped).
	d := New(db, nil)
	if _, err := d.Reveal(ctx, id); err == nil {
		t.Fatal("expected failure when VAYU_SECRET is missing")
	}
}

// TestRewrapMasterMigratesWithoutDataLoss proves the encryption secret can be
// changed in place (e.g. introducing or rotating VAYU_SECRET) with zero re-entry.
func TestRewrapMasterMigratesWithoutDataLoss(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	a := New(db, nil) // start self-managed (no VAYU_SECRET)
	id, err := a.Upsert(ctx, ProviderN8N, "n8n automation", "https://n8n.example/webhook", "tok-123", true, false)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Operator introduces a dedicated encryption secret — re-wrap in place.
	if err := a.RewrapMaster([]byte("new-vayu-secret")); err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	// The same store still reads the secret (DEK unchanged in memory).
	if got, err := a.Reveal(ctx, id); err != nil || got != "tok-123" {
		t.Fatalf("reveal after rewrap (same store): got %q err %v", got, err)
	}
	// And a fresh store with the new secret reads it; the old (no-secret) path fails.
	if got, err := New(db, []byte("new-vayu-secret")).Reveal(ctx, id); err != nil || got != "tok-123" {
		t.Fatalf("reveal after rewrap (new store, new secret): got %q err %v", got, err)
	}
	if _, err := New(db, nil).Reveal(ctx, id); err == nil {
		t.Fatal("expected failure: keyring is now env-wrapped, so no-secret access must fail")
	}
}
