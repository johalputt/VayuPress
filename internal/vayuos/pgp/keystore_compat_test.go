package pgp

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestKeystoreLegacyFilenameStillLoads guards the security-hardening refactor
// that stopped naming key files after a bare hash of the (email-bearing)
// userID: a file written under the OLD scheme must still load, because lookup
// now goes through an index built from file CONTENTS, not the file name. It also
// checks a re-save reuses that file rather than writing a duplicate under the
// new name.
func TestKeystoreLegacyFilenameStillLoads(t *testing.T) {
	dir := t.TempDir()
	secret := []byte("master-secret-under-test")
	ks, err := newKeyStore(dir, secret)
	if err != nil {
		t.Fatalf("new keystore: %v", err)
	}
	userID := "mail:legacy@example.com"
	rec := storedKey{UserID: userID, Email: "legacy@example.com", Name: "Legacy", Fingerprint: "FP1", PublicArmor: "pub"}
	if err := ks.save(rec, []byte("PRIVATE-KEY-BYTES")); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Rename the freshly written file to the OLD bare-sha256 scheme to simulate
	// a file created before the refactor.
	cur := filepath.Join(dir, ks.fileByID[userID])
	sum := sha256.Sum256([]byte(userID))
	legacy := filepath.Join(dir, hex.EncodeToString(sum[:])+".key.json")
	if err := os.Rename(cur, legacy); err != nil {
		t.Fatalf("rename to legacy: %v", err)
	}

	// A fresh keystore must find and decrypt the legacy-named file by contents.
	ks2, err := newKeyStore(dir, secret)
	if err != nil {
		t.Fatalf("reopen keystore: %v", err)
	}
	_, priv, err := ks2.load(userID)
	if err != nil {
		t.Fatalf("load legacy key: %v", err)
	}
	if string(priv) != "PRIVATE-KEY-BYTES" {
		t.Fatalf("legacy private key mismatch: %q", priv)
	}
	if _, ok := ks2.userIDForEmail("legacy@example.com"); !ok {
		t.Fatalf("email index did not pick up the legacy file")
	}

	// A re-save must write back to the SAME (legacy) file, not create a second.
	rec.Name = "Legacy Updated"
	if err := ks2.save(rec, []byte("PRIVATE-KEY-BYTES-2")); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".key.json") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 key file after re-save, got %d", n)
	}
}

// TestKeystoreFilenameNotBareHashOfUserID confirms the fix itself: a new key's
// file name is NOT the bare SHA-256 of the userID (which anyone could recompute
// from a guessed address), but the keyed-HMAC token instead.
func TestKeystoreFilenameNotBareHashOfUserID(t *testing.T) {
	dir := t.TempDir()
	ks, err := newKeyStore(dir, []byte("secret"))
	if err != nil {
		t.Fatalf("new keystore: %v", err)
	}
	userID := "mail:secret-user@example.com"
	if err := ks.save(storedKey{UserID: userID, Email: "secret-user@example.com"}, []byte("x")); err != nil {
		t.Fatalf("save: %v", err)
	}
	sum := sha256.Sum256([]byte(userID))
	bare := hex.EncodeToString(sum[:]) + ".key.json"
	if _, err := os.Stat(filepath.Join(dir, bare)); err == nil {
		t.Fatalf("new key file must not use the bare-sha256-of-userID name")
	}
	if ks.fileByID[userID] == bare {
		t.Fatalf("filename is still the bare hash of the userID")
	}
}
