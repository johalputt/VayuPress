package pgp

import (
	"bytes"
	"testing"
)

// EnsureKeypair generates a key the first time and is a no-op (same key) on
// subsequent calls for the same email — so it is safe to run on every account
// creation and as a boot-time backfill.
func TestEnsureKeypairIdempotent(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	first, err := e.EnsureKeypair(&PGPUser{UserID: "mail:dave@example.com", Name: "Dave", Email: "dave@example.com"})
	if err != nil {
		t.Fatalf("ensure (create): %v", err)
	}
	if first.Fingerprint == "" {
		t.Fatalf("expected a fingerprint")
	}
	second, err := e.EnsureKeypair(&PGPUser{UserID: "mail:dave@example.com", Name: "Dave", Email: "dave@example.com"})
	if err != nil {
		t.Fatalf("ensure (existing): %v", err)
	}
	if second.Fingerprint != first.Fingerprint {
		t.Fatalf("ensure must not regenerate: %s != %s", second.Fingerprint, first.Fingerprint)
	}
	keys, _ := e.ListKeys()
	if len(keys) != 1 {
		t.Fatalf("expected exactly 1 stored key, got %d", len(keys))
	}
}

// EnsureKeypair resolves an existing key by EMAIL even when a different userID
// is supplied, so a CMS user and a mail account sharing an address never end up
// with two competing keys.
func TestEnsureKeypairMatchesByEmail(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	orig, err := e.GenerateKeypair(&PGPUser{UserID: "cms-42", Name: "Eve", Email: "eve@example.com"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got, err := e.EnsureKeypair(&PGPUser{UserID: "mail:eve@example.com", Name: "Eve", Email: "eve@example.com"})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got.Fingerprint != orig.Fingerprint {
		t.Fatalf("ensure should reuse the existing key for the email")
	}
	if got.UserID != "cms-42" {
		t.Fatalf("expected existing userID cms-42, got %q", got.UserID)
	}
}

func TestEnsureKeypairValidation(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	if _, err := e.EnsureKeypair(nil); err == nil {
		t.Fatalf("nil user must error")
	}
	if _, err := e.EnsureKeypair(&PGPUser{UserID: "x"}); err == nil {
		t.Fatalf("empty email must error")
	}
}

// DecryptForEmail must transparently resolve the recipient email to its key and
// decrypt, without the caller knowing the internal userID.
func TestDecryptForEmailRoundtrip(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	if _, err := e.EnsureKeypair(&PGPUser{UserID: "mail:fred@example.com", Name: "Fred", Email: "fred@example.com"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	msg := []byte("transparent inbox decryption 🛡")
	ct, err := e.Encrypt(msg, "fred@example.com")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	pt, err := e.DecryptForEmail(ct, "Fred@Example.com") // case-insensitive
	if err != nil {
		t.Fatalf("decrypt for email: %v", err)
	}
	if !bytes.Equal(pt, msg) {
		t.Fatalf("roundtrip mismatch: %q != %q", pt, msg)
	}
}

func TestDecryptForEmailUnknownRecipient(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	if _, err := e.DecryptForEmail([]byte("whatever"), "nobody@example.com"); err == nil {
		t.Fatalf("expected ErrNotFound for an unknown recipient")
	}
}
