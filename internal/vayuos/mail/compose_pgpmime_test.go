package mail

import (
	"context"
	"strings"
	"testing"
)

// TestComposeRichPGPMIMEWithAttachments proves the headline capability: an opt-in
// encrypted message that carries an attachment is emitted as RFC 3156 PGP/MIME
// (multipart/encrypted), with the body AND the attachment sealed inside the
// ciphertext — never in the clear. This is exactly what inline PGP could not do.
func TestComposeRichPGPMIMEWithAttachments(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, encryptingBridge{loopbackBridge{localSet: map[string]bool{"bob@example.com": true}}})
	if _, err := e.ComposeRich(context.Background(), ComposeMessage{
		From: "alice@example.com", To: []string{"bob@example.com"}, Subject: "secret",
		Body: "TOP-SECRET-BODY", Encrypt: true,
		Attachments: []Attachment{{Filename: "shot.png", ContentType: "image/png", Data: []byte("SECRETPIXELS")}},
	}); err != nil {
		t.Fatalf("ComposeRich: %v", err)
	}
	raw := readMaildirRaw(t, e.cfg.StorageDir)
	for _, want := range []string{"multipart/encrypted", `protocol="application/pgp-encrypted"`, "application/pgp-encrypted", "Version: 1", "X-VayuPGP"} {
		if !strings.Contains(raw, want) {
			t.Errorf("PGP/MIME output missing %q:\n%s", want, raw)
		}
	}
	// The body and the attachment (even its filename) live inside the encrypted
	// entity, so none of it may appear in the transmitted message.
	if strings.Contains(raw, "TOP-SECRET-BODY") {
		t.Error("plaintext body leaked outside the encrypted part")
	}
	if strings.Contains(raw, "shot.png") {
		t.Error("attachment filename leaked outside the encrypted part")
	}
}

// TestComposeRichPGPMIMEMultiRecipient proves encryption now works with Cc — the
// message is encrypted to every recipient. Recipient addresses aren't secret, so
// the Cc header stays visible; the content is sealed.
func TestComposeRichPGPMIMEMultiRecipient(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, encryptingBridge{loopbackBridge{localSet: map[string]bool{"bob@example.com": true, "carol@example.com": true}}})
	if _, err := e.ComposeRich(context.Background(), ComposeMessage{
		From: "alice@example.com", To: []string{"bob@example.com"}, CC: []string{"carol@example.com"},
		Subject: "team", Body: "hello team", Encrypt: true,
	}); err != nil {
		t.Fatalf("ComposeRich: %v", err)
	}
	raw := readMaildirRaw(t, e.cfg.StorageDir)
	if !strings.Contains(raw, "multipart/encrypted") {
		t.Errorf("a Cc'd encrypted message must be PGP/MIME:\n%s", raw)
	}
	if !strings.Contains(raw, "Cc: carol@example.com") {
		t.Errorf("the Cc header should still be present:\n%s", raw)
	}
	if strings.Contains(raw, "hello team") {
		t.Error("body leaked in the clear")
	}
}

// TestComposeRichEncryptFallsBackWithoutKeys pins the safety rule: if a recipient
// has no resolvable key, an opt-in encrypt falls back to honest plaintext rather
// than sending ciphertext the recipient could never read.
func TestComposeRichEncryptFallsBackWithoutKeys(t *testing.T) {
	t.Parallel()
	// loopbackBridge.EncryptForRecipients returns ok=false (no keys on file).
	e := newLoopbackEngine(t, loopbackBridge{localSet: map[string]bool{"bob@example.com": true}})
	if _, err := e.ComposeRich(context.Background(), ComposeMessage{
		From: "alice@example.com", To: []string{"bob@example.com"}, Subject: "hi",
		Body: "READABLE-BODY", Encrypt: true,
	}); err != nil {
		t.Fatalf("ComposeRich: %v", err)
	}
	raw := readMaildirRaw(t, e.cfg.StorageDir)
	if strings.Contains(raw, "multipart/encrypted") || strings.Contains(raw, "X-VayuPGP") {
		t.Errorf("must fall back to plaintext when a recipient has no key:\n%s", raw)
	}
	if !strings.Contains(raw, "READABLE-BODY") {
		t.Errorf("plaintext body must be readable on fallback:\n%s", raw)
	}
}
