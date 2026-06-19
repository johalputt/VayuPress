package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifyChecksum(t *testing.T) {
	data := []byte("hello vayupress")
	sum := sha256.Sum256(data)
	if err := VerifyChecksum(data, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("expected pass: %v", err)
	}
	if err := VerifyChecksum(data, hex.EncodeToString(sum[:])[:62]+"00"); err == nil {
		t.Error("expected mismatch failure")
	}
	if err := VerifyChecksum(data, "not-hex"); err == nil {
		t.Error("expected decode failure")
	}
}

func TestVerifySignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("the binary bytes")
	digest := sha256.Sum256(data)
	sig := ed25519.Sign(priv, digest[:])
	pubHex := hex.EncodeToString(pub)
	sigHex := hex.EncodeToString(sig)

	if err := VerifySignature(pubHex, data, sigHex); err != nil {
		t.Fatalf("expected valid signature: %v", err)
	}

	// Tamper with data → fail.
	if err := VerifySignature(pubHex, []byte("tampered"), sigHex); err == nil {
		t.Error("expected failure on tampered data")
	}

	// Empty pubkey → error.
	if err := VerifySignature("", data, sigHex); err == nil {
		t.Error("expected error on empty pubkey")
	}
}
