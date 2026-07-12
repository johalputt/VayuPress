package pgp

import (
	"bytes"
	"testing"
)

// TestEncryptAndSignFromEmailRoundtrip proves the VayuTalk web path: the server
// signs+encrypts a message on behalf of the sender mailbox (by address alone),
// and the recipient mailbox decrypts it — exactly the round trip the SSE bridge
// performs for the browser. This is the same wire shape VayuMail Mobile produces
// with keyring.Encrypt(plaintext, [peer], selfEmail), so a web sender and an app
// sender are interchangeable.
func TestEncryptAndSignFromEmailRoundtrip(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	if _, err := e.EnsureKeypair(&PGPUser{UserID: "mail:alice@example.com", Name: "Alice", Email: "alice@example.com"}); err != nil {
		t.Fatalf("ensure alice: %v", err)
	}
	if _, err := e.EnsureKeypair(&PGPUser{UserID: "mail:bob@example.com", Name: "Bob", Email: "bob@example.com"}); err != nil {
		t.Fatalf("ensure bob: %v", err)
	}

	msg := []byte("hello from the web console 🔒 — vanishes when read")
	ct, err := e.EncryptAndSignFromEmail(msg, "bob@example.com", "Alice@Example.com") // sender by address, case-insensitive
	if err != nil {
		t.Fatalf("encrypt+sign from email: %v", err)
	}
	if bytes.Contains(ct, msg) {
		t.Fatalf("ciphertext must not contain the plaintext")
	}

	pt, err := e.DecryptForEmail(ct, "bob@example.com")
	if err != nil {
		t.Fatalf("recipient decrypt: %v", err)
	}
	if !bytes.Equal(pt, msg) {
		t.Fatalf("roundtrip mismatch: %q != %q", pt, msg)
	}
}

// TestEncryptAndSignFromEmailUnknownSender ensures a mailbox with no local key
// cannot sign — the handler treats this as a clean "no key" error and mints one.
func TestEncryptAndSignFromEmailUnknownSender(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	if _, err := e.EnsureKeypair(&PGPUser{UserID: "mail:carol@example.com", Name: "Carol", Email: "carol@example.com"}); err != nil {
		t.Fatalf("ensure carol: %v", err)
	}
	if _, err := e.EncryptAndSignFromEmail([]byte("hi"), "carol@example.com", "ghost@example.com"); err == nil {
		t.Fatalf("expected error signing as a mailbox with no key")
	}
}
