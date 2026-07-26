// SPDX-License-Identifier: Apache-2.0

package pgp

// keyenvelope.go — envelope encryption for the VayuPGP keystore's at-rest key
// (audit M8).
//
// Previously the AES-256-GCM key that seals every user's PGP PRIVATE key was
// derived directly from the host's API key: aeadKey = sha256("…v1\0" || API_KEY).
// The API key is a wire-exposed authentication credential (X-API-Key on every
// admin/REST call), so coupling the confidentiality of the most sensitive
// material in the system to it is wrong — and rotating the API key would BRICK
// every stored key, since the derivation was frozen with no rewrap.
//
// Now a random, persistent Data Encryption Key (DEK) is the real aeadKey. The
// DEK is stored once, WRAPPED by a Key Encryption Key (KEK) that comes from a
// dedicated at-rest secret (VAYU_SECRET, else a host-bound key file), never from
// the API key. On first boot with existing keys the DEK is SET EQUAL to the
// legacy API-key-derived key, so every stored private key still decrypts with no
// re-encryption; from then on the DEK is independent, so the API key rotates
// freely and the KEK can be rotated in place without touching a single key file.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/johalputt/vayupress/internal/logging"
)

const (
	// dekKeyringName is the wrapped-DEK file kept in the keystore directory.
	dekKeyringName = ".keyring.json"
	// kekCheckPlain is sealed under the KEK so a wrong/changed KEK is detected
	// before it silently produces garbage plaintext.
	kekCheckPlain = "vayupgp-keystore-kek-check-v1"
)

// dekKeyring is the on-disk wrapped-DEK record. When Src=="none" the DEK is the
// raw hex (host-sandbox protection only, warned); otherwise DEK/Check are sealed
// blobs ("nonceHex.ctHex") opened with the resolved KEK.
type dekKeyring struct {
	DEK   string `json:"dek"`
	Check string `json:"check"`
	Src   string `json:"src"` // "env" | "file" | "none"
}

// deriveKEK domain-separates the KEK from any other use of the secret.
func deriveKEK(secret []byte) [32]byte {
	return sha256.Sum256(append([]byte("vayupgp-kek-v1\x00"), secret...))
}

// resolveKEK returns the 32-byte KEK from the strongest available source:
// kekSecret (VAYU_SECRET, "env") or a host-bound key file ("file"). create lets
// it generate the key file on first use. ok is false when neither is available.
func resolveKEK(kekSecret []byte, kekFilePath string, create bool) (kek [32]byte, src string, ok bool) {
	if len(kekSecret) > 0 {
		return deriveKEK(kekSecret), "env", true
	}
	if secret, got := loadKEKFile(kekFilePath, create); got {
		return deriveKEK(secret), "file", true
	}
	return [32]byte{}, "none", false
}

// loadKEKFile reads (or, when create is set, generates) the 32-byte host-bound
// KEK secret from a 0600 file outside the keystore data.
func loadKEKFile(path string, create bool) ([]byte, bool) {
	if strings.TrimSpace(path) == "" {
		return nil, false
	}
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		return b[:32], true
	}
	if !create {
		return nil, false
	}
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return nil, false
	}
	return buf, true
}

func sealKEK(kek [32]byte, plaintext []byte) (string, error) {
	block, err := aes.NewCipher(kek[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	return hex.EncodeToString(nonce) + "." + hex.EncodeToString(ct), nil
}

func openKEK(kek [32]byte, blob string) ([]byte, error) {
	parts := strings.SplitN(blob, ".", 2)
	if len(parts) != 2 {
		return nil, errors.New("vayupgp: malformed wrapped blob")
	}
	nonce, err := hex.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	ct, err := hex.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(kek[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("vayupgp: bad nonce length")
	}
	return gcm.Open(nil, nonce, ct, nil)
}

// loadOrCreateDEK returns the keystore's at-rest DEK. It loads the wrapped DEK
// when present; otherwise it mints one — migrating to the legacy API-key-derived
// key when key files already exist (so nothing needs re-encrypting), or a fresh
// random key on a clean install — and persists it wrapped (audit M8).
func loadOrCreateDEK(dir string, kekSecret []byte, kekFilePath string, legacy [32]byte) ([32]byte, error) {
	var zero [32]byte
	path := filepath.Join(dir, dekKeyringName)

	if raw, err := os.ReadFile(path); err == nil {
		var kr dekKeyring
		if err := json.Unmarshal(raw, &kr); err != nil {
			return zero, fmt.Errorf("vayupgp: parse keystore keyring: %w", err)
		}
		if kr.Src == "none" {
			b, err := hex.DecodeString(kr.DEK)
			if err != nil || len(b) != 32 {
				return zero, errors.New("vayupgp: corrupt plaintext keystore key")
			}
			var dek [32]byte
			copy(dek[:], b)
			return dek, nil
		}
		kek, _, ok := resolveKEK(kekSecret, kekFilePath, false)
		if !ok {
			return zero, errors.New("vayupgp: keystore key is wrapped but its KEK is unavailable (set VAYU_SECRET or restore the KEK file)")
		}
		if _, err := openKEK(kek, kr.Check); err != nil {
			return zero, errors.New("vayupgp: the encryption secret does not match the stored keystore key")
		}
		b, err := openKEK(kek, kr.DEK)
		if err != nil || len(b) != 32 {
			return zero, errors.New("vayupgp: could not unwrap the keystore key")
		}
		var dek [32]byte
		copy(dek[:], b)
		return dek, nil
	}

	// No keyring yet: migrate existing keys (DEK := legacy so their ciphertext
	// stays decryptable) or, on a clean install, mint a fresh random DEK.
	var dek [32]byte
	if hasKeyFiles(dir) {
		dek = legacy
		logging.LogInfo("vayupgp", "migrating keystore to envelope encryption; existing keys decrypt unchanged and the API key is no longer the at-rest key (audit M8)")
	} else {
		if _, err := io.ReadFull(rand.Reader, dek[:]); err != nil {
			return zero, err
		}
	}
	if err := persistDEK(path, dek, kekSecret, kekFilePath); err != nil {
		return zero, err
	}
	return dek, nil
}

// persistDEK writes the DEK wrapped by the resolved KEK; if no KEK is available
// it writes the DEK unwrapped in a 0600 file and warns loudly.
func persistDEK(path string, dek [32]byte, kekSecret []byte, kekFilePath string) error {
	var kr dekKeyring
	if kek, src, ok := resolveKEK(kekSecret, kekFilePath, true); ok {
		wrapped, err := sealKEK(kek, dek[:])
		if err != nil {
			return err
		}
		check, err := sealKEK(kek, []byte(kekCheckPlain))
		if err != nil {
			return err
		}
		kr = dekKeyring{DEK: wrapped, Check: check, Src: src}
	} else {
		logging.LogWarn("vayupgp", "keystore encryption key stored UNWRAPPED on disk: set VAYU_SECRET (or provide a writable KEK file) so the key that seals every mailbox's PGP private key lives behind a dedicated at-rest secret, not beside the ciphertext")
		kr = dekKeyring{DEK: hex.EncodeToString(dek[:]), Src: "none"}
	}
	data, err := json.MarshalIndent(kr, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// hasKeyFiles reports whether the directory already holds stored keypairs (used
// to decide migrate-vs-fresh on first envelope boot).
func hasKeyFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".key.json") {
			return true
		}
	}
	return false
}
