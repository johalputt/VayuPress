package mail

import (
	"context"
	"strings"
	"testing"
)

// TestComposeRichEncryptOptIn pins the fix for silent auto-encryption: the admin
// composer (ComposeRich) must only PGP-encrypt when the operator opts in
// (Encrypt:true). Encrypting to a recipient that cannot decrypt — e.g. a plain
// Gmail address — would arrive as an unreadable ciphertext block and score as
// spam, which is exactly what happened before.
func TestComposeRichEncryptOptIn(t *testing.T) {
	t.Parallel()

	// Default (Encrypt:false) → plaintext, even though the bridge CAN encrypt.
	ePlain := newLoopbackEngine(t, encryptingBridge{loopbackBridge{localSet: map[string]bool{"bob@example.com": true}}})
	if _, err := ePlain.ComposeRich(context.Background(), ComposeMessage{
		From: "alice@example.com", To: []string{"bob@example.com"}, Subject: "hi", Body: "plain hello",
	}); err != nil {
		t.Fatalf("ComposeRich (plain): %v", err)
	}
	raw := readMaildirRaw(t, ePlain.cfg.StorageDir)
	if strings.Contains(raw, "X-VayuPGP") || strings.Contains(raw, "BEGIN PGP MESSAGE") {
		t.Fatalf("a default send must NOT be encrypted:\n%s", raw)
	}
	if !strings.Contains(raw, "plain hello") {
		t.Fatalf("plaintext body missing from the delivered/Sent copy:\n%s", raw)
	}

	// Opt-in (Encrypt:true) with a keyed recipient → encrypted, marker set.
	eEnc := newLoopbackEngine(t, encryptingBridge{loopbackBridge{localSet: map[string]bool{"bob@example.com": true}}})
	if _, err := eEnc.ComposeRich(context.Background(), ComposeMessage{
		From: "alice@example.com", To: []string{"bob@example.com"}, Subject: "secret", Body: "secret hello", Encrypt: true,
	}); err != nil {
		t.Fatalf("ComposeRich (encrypt): %v", err)
	}
	if rawE := readMaildirRaw(t, eEnc.cfg.StorageDir); !strings.Contains(rawE, "X-VayuPGP") {
		t.Fatalf("an opt-in encrypted send must set the X-VayuPGP marker:\n%s", rawE)
	}
}
