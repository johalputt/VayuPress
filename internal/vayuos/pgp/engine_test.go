package pgp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := DefaultConfig()
	cfg.StorageDir = t.TempDir()
	cfg.MasterSecret = []byte("test-master-secret-do-not-use-in-prod")
	e := NewEngine(&cfg)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	return e
}

func TestGenerateKeypair(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	kp, err := e.GenerateKeypair(&PGPUser{UserID: "u1", Name: "Alice", Email: "alice@example.com"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(kp.Fingerprint) < 32 {
		t.Errorf("fingerprint too short: %q", kp.Fingerprint)
	}
	if !strings.Contains(kp.PublicArmor, "PGP PUBLIC KEY BLOCK") {
		t.Errorf("public armor missing header")
	}
	if strings.Contains(kp.PublicArmor, "PRIVATE") {
		t.Errorf("public armor must not contain private material")
	}
	want := time.Now().Add(2 * 365 * 24 * time.Hour)
	if kp.ExpiresAt.Before(want.Add(-48*time.Hour)) || kp.ExpiresAt.After(want.Add(48*time.Hour)) {
		t.Errorf("expiry not ~2y: %v", kp.ExpiresAt)
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	if _, err := e.GenerateKeypair(&PGPUser{UserID: "bob", Name: "Bob", Email: "bob@example.com"}); err != nil {
		t.Fatalf("gen: %v", err)
	}
	msg := []byte("sovereign secret message 🛡")
	ct, err := e.Encrypt(msg, "bob@example.com")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(ct, msg) {
		t.Fatalf("ciphertext leaks plaintext")
	}
	if !strings.Contains(string(ct), "PGP MESSAGE") {
		t.Fatalf("ciphertext not armored")
	}
	pt, err := e.Decrypt(ct, "bob")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(pt, msg) {
		t.Fatalf("roundtrip mismatch: %q != %q", pt, msg)
	}
}

func TestSignVerifyRoundtrip(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	if _, err := e.GenerateKeypair(&PGPUser{UserID: "carol", Name: "Carol", Email: "carol@example.com"}); err != nil {
		t.Fatalf("gen: %v", err)
	}
	data := []byte("attest this content")
	sig, err := e.Sign(data, "carol")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	ok, err := e.Verify(data, sig, "carol@example.com")
	if err != nil || !ok {
		t.Fatalf("verify valid sig: ok=%v err=%v", ok, err)
	}
	ok, _ = e.Verify([]byte("tampered"), sig, "carol@example.com")
	if ok {
		t.Fatalf("verify must fail for tampered data")
	}
}

func TestEncryptAndSign(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	_, _ = e.GenerateKeypair(&PGPUser{UserID: "snd", Name: "Sender", Email: "snd@example.com"})
	_, _ = e.GenerateKeypair(&PGPUser{UserID: "rcv", Name: "Receiver", Email: "rcv@example.com"})
	msg := []byte("signed and sealed")
	ct, err := e.EncryptAndSign(msg, "rcv@example.com", "snd")
	if err != nil {
		t.Fatalf("encrypt+sign: %v", err)
	}
	pt, err := e.Decrypt(ct, "rcv")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(pt, msg) {
		t.Fatalf("mismatch")
	}
}

func TestKeystoreEncryptedAtRest(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	kp, _ := e.GenerateKeypair(&PGPUser{UserID: "rest", Name: "Rest", Email: "rest@example.com"})
	// The decrypted private entity must never appear in the on-disk file.
	rec, priv, err := e.ks.load("rest")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !strings.Contains(string(priv), "PGP PRIVATE KEY BLOCK") {
		t.Fatalf("expected armored private after decrypt")
	}
	if strings.Contains(rec.PrivateCT, "PRIVATE KEY") {
		t.Fatalf("private key stored in plaintext")
	}
	if rec.Fingerprint != kp.Fingerprint {
		t.Fatalf("fingerprint mismatch")
	}
	// Wrong master secret must fail to decrypt.
	bad := DefaultConfig()
	bad.StorageDir = e.cfg.StorageDir
	bad.MasterSecret = []byte("the-wrong-secret")
	be := NewEngine(&bad)
	if err := be.Start(context.Background()); err != nil {
		t.Fatalf("start bad: %v", err)
	}
	if _, err := be.entity("rest"); err == nil {
		t.Fatalf("decryption must fail with wrong master secret")
	}
}

func TestRotationPreservesOldMessages(t *testing.T) {
	t.Parallel()
	e := newTestEngine(t)
	_, _ = e.GenerateKeypair(&PGPUser{UserID: "rot", Name: "Rot", Email: "rot@example.com"})
	msg := []byte("encrypted before rotation")
	ct, err := e.Encrypt(msg, "rot@example.com")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := e.RotateKeypair("rot"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	// Drop the in-memory cache so decryption must rely on stored (current +
	// archived) keys.
	e.mu.Lock()
	e.unlocked = make(map[string]*openpgp.Entity)
	e.mu.Unlock()
	pt, err := e.Decrypt(ct, "rot")
	if err != nil {
		t.Fatalf("decrypt after rotation: %v", err)
	}
	if !bytes.Equal(pt, msg) {
		t.Fatalf("old message lost after rotation")
	}
	// New encryption must use the new key and still roundtrip.
	ct2, err := e.Encrypt([]byte("after"), "rot@example.com")
	if err != nil {
		t.Fatalf("encrypt after rotate: %v", err)
	}
	if _, err := e.Decrypt(ct2, "rot"); err != nil {
		t.Fatalf("decrypt new: %v", err)
	}
}

func TestWKDLocalHashKnownVector(t *testing.T) {
	t.Parallel()
	// Known WKD vector: "Joe.Doe" hashes to "iy9q119eutrkn8s1mk4r39qejnbu3n5q".
	got := wkdLocalHash("Joe.Doe")
	want := "iy9q119eutrkn8s1mk4r39qejnbu3n5q"
	if got != want {
		t.Fatalf("wkd hash mismatch: got %s want %s", got, want)
	}
}

func TestZbase32(t *testing.T) {
	t.Parallel()
	if zbase32([]byte{0}) == "" {
		t.Fatalf("zbase32 empty")
	}
}
