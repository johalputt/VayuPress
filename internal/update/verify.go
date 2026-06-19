package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
)

// VerifyChecksum computes the SHA-256 of data and compares it (constant-time)
// against the hex-encoded expected digest.
func VerifyChecksum(data []byte, expectedHex string) error {
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return fmt.Errorf("update: decode checksum: %w", err)
	}
	if len(expected) != sha256.Size {
		return fmt.Errorf("update: checksum length %d != %d", len(expected), sha256.Size)
	}
	sum := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(sum[:], expected) != 1 {
		return errors.New("update: checksum mismatch")
	}
	return nil
}

// VerifySignature verifies an Ed25519 signature over the SHA-256 digest of
// data. pubKeyHex is the pinned release-signing public key (hex). sigHex is the
// hex-encoded signature. An empty pubkey is rejected with a clear error.
func VerifySignature(pubKeyHex string, data []byte, sigHex string) error {
	if pubKeyHex == "" {
		return errors.New("update: pinned release public key is empty — refusing to verify")
	}
	pub, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return fmt.Errorf("update: decode pubkey: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("update: pubkey length %d != %d", len(pub), ed25519.PublicKeySize)
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return fmt.Errorf("update: decode signature: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("update: signature length %d != %d", len(sig), ed25519.SignatureSize)
	}
	sum := sha256.Sum256(data)
	if !ed25519.Verify(ed25519.PublicKey(pub), sum[:], sig) {
		return errors.New("update: invalid signature")
	}
	return nil
}
