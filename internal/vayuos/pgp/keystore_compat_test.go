// SPDX-License-Identifier: Apache-2.0

package pgp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLegacyKeystoreMigratesToDEK is the M8 bricking-safety guard: a pre-M8
// store (key files sealed under the API-key-derived legacy key, NO .keyring.json)
// must open under the envelope scheme with the DEK set to the legacy key, so
// every stored private key still decrypts — and a keyring file is written so the
// API key stops being the at-rest key.
func TestLegacyKeystoreMigratesToDEK(t *testing.T) {
	dir := t.TempDir()
	master := []byte("legacy-api-key")
	legacy := sha256.Sum256(append([]byte("vayupgp-keystore-v1\x00"), master...))

	priv := []byte("-----BEGIN PGP PRIVATE KEY BLOCK-----\nlegacy-material\n-----END PGP PRIVATE KEY BLOCK-----")
	nonceHex, ctHex := sealAESGCM(t, legacy, priv)
	rec := storedKey{
		UserID: "mail:old@example.com", Email: "old@example.com", Fingerprint: "FP-OLD",
		PrivateNonce: nonceHex, PrivateCT: ctHex, CreatedAt: time.Now().UTC(),
	}
	data, _ := json.MarshalIndent(rec, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "legacyfile.key.json"), data, 0o600); err != nil {
		t.Fatalf("seed legacy file: %v", err)
	}
	// No .keyring.json exists yet → this is a pre-M8 store.

	ks, err := newKeyStore(dir, master, nil, "")
	if err != nil {
		t.Fatalf("open (migrate): %v", err)
	}
	_, got, err := ks.load("mail:old@example.com")
	if err != nil {
		t.Fatalf("legacy key must still decrypt after migration: %v", err)
	}
	if string(got) != string(priv) {
		t.Fatalf("decrypted mismatch: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, dekKeyringName)); err != nil {
		t.Fatalf("migration must write a keyring file: %v", err)
	}

	// Reopen with a ROTATED API key: the DEK is now persisted, so it still opens.
	ks2, err := newKeyStore(dir, []byte("rotated-api-key"), nil, "")
	if err != nil {
		t.Fatalf("reopen after rotation: %v", err)
	}
	if _, got2, err := ks2.load("mail:old@example.com"); err != nil || string(got2) != string(priv) {
		t.Fatalf("rotated API key must still decrypt: got %q err %v", got2, err)
	}
}

// sealAESGCM seals plaintext under a 32-byte key, returning hex nonce and
// ciphertext exactly as the keystore stores them.
func sealAESGCM(t *testing.T, key [32]byte, plaintext []byte) (nonceHex, ctHex string) {
	t.Helper()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	return hex.EncodeToString(nonce), hex.EncodeToString(ct)
}

// TestKeystoreDeterministicEmailResolution guards against the "active key flips
// on restart" hazard: if two key files ever share an email, reindex must resolve
// the same one every time (the oldest), so a message encrypted to — or a
// fingerprint verified against — that key never silently breaks after a reboot.
func TestKeystoreDeterministicEmailResolution(t *testing.T) {
	dir := t.TempDir()
	secret := []byte("s")
	ks, err := newKeyStore(dir, secret, nil, "")
	if err != nil {
		t.Fatalf("new keystore: %v", err)
	}
	older := storedKey{UserID: "user-older", Email: "dup@example.com", CreatedAt: time.Unix(1000, 0)}
	newer := storedKey{UserID: "user-newer", Email: "dup@example.com", CreatedAt: time.Unix(2000, 0)}
	if err := ks.save(older, []byte("OLD")); err != nil {
		t.Fatalf("save older: %v", err)
	}
	if err := ks.save(newer, []byte("NEW")); err != nil {
		t.Fatalf("save newer: %v", err)
	}
	// IN-PROCESS (post-save) resolution must ALSO be the oldest key — not the
	// last one saved. This is the crux: the web-encrypt path and the device
	// key-sync path both read this index in the SAME running process, so if save
	// disagreed with reindex the two could target different keys and a
	// web-composed message would never decrypt on the device.
	if id, ok := ks.userIDForEmail("dup@example.com"); !ok || id != "user-older" {
		t.Fatalf("in-process resolution = %q, want user-older (save must match reindex)", id)
	}
	// Every fresh open (reindex) must land on the same oldest key.
	for i := 0; i < 5; i++ {
		ks2, err := newKeyStore(dir, secret, nil, "")
		if err != nil {
			t.Fatalf("reopen %d: %v", i, err)
		}
		id, ok := ks2.userIDForEmail("dup@example.com")
		if !ok || id != "user-older" {
			t.Fatalf("resolution flipped on reopen %d: got %q, want user-older", i, id)
		}
	}
}

// TestKeystoreRevokedKeyNotActive verifies a revoked key never becomes the
// active key for its address — not at save time and not after a restart's
// reindex — so the web never encrypts to a revoked key.
func TestKeystoreRevokedKeyNotActive(t *testing.T) {
	dir := t.TempDir()
	secret := []byte("s")
	ks, err := newKeyStore(dir, secret, nil, "")
	if err != nil {
		t.Fatalf("new keystore: %v", err)
	}
	live := storedKey{UserID: "live", Email: "r@example.com", CreatedAt: time.Unix(2000, 0)}
	revoked := storedKey{UserID: "revoked", Email: "r@example.com", CreatedAt: time.Unix(1000, 0), Revoked: true}
	if err := ks.save(live, []byte("LIVE")); err != nil {
		t.Fatalf("save live: %v", err)
	}
	if err := ks.save(revoked, []byte("REVOKED")); err != nil {
		t.Fatalf("save revoked: %v", err)
	}
	// The revoked key is OLDER, but it must not win — the live key stays active.
	if id, ok := ks.userIDForEmail("r@example.com"); !ok || id != "live" {
		t.Fatalf("in-process active = %q, want live (revoked must not win)", id)
	}
	ks2, err := newKeyStore(dir, secret, nil, "")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if id, ok := ks2.userIDForEmail("r@example.com"); !ok || id != "live" {
		t.Fatalf("post-reindex active = %q, want live (revoked must not re-activate)", id)
	}
}

// TestKeystoreLegacyFilenameStillLoads guards the security-hardening refactor
// that stopped naming key files after a bare hash of the (email-bearing)
// userID: a file written under the OLD scheme must still load, because lookup
// now goes through an index built from file CONTENTS, not the file name. It also
// checks a re-save reuses that file rather than writing a duplicate under the
// new name.
func TestKeystoreLegacyFilenameStillLoads(t *testing.T) {
	dir := t.TempDir()
	secret := []byte("master-secret-under-test")
	ks, err := newKeyStore(dir, secret, nil, "")
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
	ks2, err := newKeyStore(dir, secret, nil, "")
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
	ks, err := newKeyStore(dir, []byte("secret"), nil, "")
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
