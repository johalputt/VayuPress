package pgp

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
	"sync"
	"time"
)

// ErrNotFound is returned when a key for the requested id/email does not exist.
var ErrNotFound = errors.New("vayupgp: key not found")

// storedKey is the on-disk representation of a keypair. The private key is held
// only as AES-256-GCM ciphertext; its plaintext never touches disk.
type storedKey struct {
	UserID       string    `json:"user_id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	Fingerprint  string    `json:"fingerprint"`
	PublicArmor  string    `json:"public_armor"`
	PrivateNonce string    `json:"private_nonce"` // hex
	PrivateCT    string    `json:"private_ct"`    // hex (AES-256-GCM ciphertext of armored private key)
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Revoked      bool      `json:"revoked"`
}

// keyStore persists encrypted keypairs to a directory and maintains an
// email→userID index for recipient lookup.
type keyStore struct {
	dir     string
	aeadKey [32]byte

	mu      sync.RWMutex
	byEmail map[string]string // email → userID
}

// newKeyStore opens (and lazily creates) a key store under dir, deriving the
// at-rest AES key from masterSecret. The derived key is held only in memory.
func newKeyStore(dir string, masterSecret []byte) (*keyStore, error) {
	if dir == "" {
		return nil, errors.New("vayupgp: empty storage dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("vayupgp: create storage dir: %w", err)
	}
	ks := &keyStore{dir: dir, byEmail: make(map[string]string)}
	// Domain-separated derivation so the keystore key is distinct from any other
	// use of the master secret. Never log or persist the derived key.
	ks.aeadKey = sha256.Sum256(append([]byte("vayupgp-keystore-v1\x00"), masterSecret...))
	if err := ks.reindex(); err != nil {
		return nil, err
	}
	return ks, nil
}

func (k *keyStore) path(userID string) string {
	sum := sha256.Sum256([]byte(userID))
	return filepath.Join(k.dir, hex.EncodeToString(sum[:])+".key.json")
}

func (k *keyStore) seal(plaintext []byte) (nonceHex, ctHex string, err error) {
	block, err := aes.NewCipher(k.aeadKey[:])
	if err != nil {
		return "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", "", err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	return hex.EncodeToString(nonce), hex.EncodeToString(ct), nil
}

func (k *keyStore) open(nonceHex, ctHex string) ([]byte, error) {
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil {
		return nil, err
	}
	ct, err := hex.DecodeString(ctHex)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k.aeadKey[:])
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

// save writes a record, encrypting privArmor at rest, and updates the index.
func (k *keyStore) save(rec storedKey, privArmor []byte) error {
	nonceHex, ctHex, err := k.seal(privArmor)
	if err != nil {
		return err
	}
	rec.PrivateNonce = nonceHex
	rec.PrivateCT = ctHex
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := k.path(rec.UserID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, k.path(rec.UserID)); err != nil {
		return err
	}
	k.mu.Lock()
	k.byEmail[normalizeEmail(rec.Email)] = rec.UserID
	k.mu.Unlock()
	return nil
}

// load reads a record and returns it plus the decrypted armored private key.
func (k *keyStore) load(userID string) (storedKey, []byte, error) {
	data, err := os.ReadFile(k.path(userID))
	if errors.Is(err, os.ErrNotExist) {
		return storedKey{}, nil, ErrNotFound
	}
	if err != nil {
		return storedKey{}, nil, err
	}
	var rec storedKey
	if err := json.Unmarshal(data, &rec); err != nil {
		return storedKey{}, nil, err
	}
	priv, err := k.open(rec.PrivateNonce, rec.PrivateCT)
	if err != nil {
		return storedKey{}, nil, fmt.Errorf("vayupgp: decrypt private key: %w", err)
	}
	return rec, priv, nil
}

// loadMeta reads a record without decrypting the private key.
func (k *keyStore) loadMeta(userID string) (storedKey, error) {
	data, err := os.ReadFile(k.path(userID))
	if errors.Is(err, os.ErrNotExist) {
		return storedKey{}, ErrNotFound
	}
	if err != nil {
		return storedKey{}, err
	}
	var rec storedKey
	if err := json.Unmarshal(data, &rec); err != nil {
		return storedKey{}, err
	}
	return rec, nil
}

// userIDForEmail resolves a recipient email to a local userID.
func (k *keyStore) userIDForEmail(email string) (string, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	id, ok := k.byEmail[normalizeEmail(email)]
	return id, ok
}

// list returns every stored record (metadata only).
func (k *keyStore) list() ([]storedKey, error) {
	entries, err := os.ReadDir(k.dir)
	if err != nil {
		return nil, err
	}
	var out []storedKey
	for _, e := range entries {
		if e.IsDir() || !hasSuffix(e.Name(), ".key.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(k.dir, e.Name()))
		if err != nil {
			continue
		}
		var rec storedKey
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// reindex rebuilds the email→userID index from disk.
func (k *keyStore) reindex() error {
	recs, err := k.list()
	if err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.byEmail = make(map[string]string, len(recs))
	for _, r := range recs {
		k.byEmail[normalizeEmail(r.Email)] = r.UserID
	}
	return nil
}

// archivePath returns the on-disk path for an archived (rotated-out) key.
func (k *keyStore) archivePath(userID, fingerprint string) string {
	sum := sha256.Sum256([]byte(userID))
	return filepath.Join(k.dir, hex.EncodeToString(sum[:])+".arch."+fingerprint+".json")
}

// archive stores a rotated-out private key so historical ciphertext encrypted
// to it stays decryptable.
func (k *keyStore) archive(rec storedKey, privArmor []byte) error {
	nonceHex, ctHex, err := k.seal(privArmor)
	if err != nil {
		return err
	}
	rec.PrivateNonce = nonceHex
	rec.PrivateCT = ctHex
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(k.archivePath(rec.UserID, rec.Fingerprint), data, 0o600)
}

// archivedPrivs returns the decrypted armored private keys archived for userID.
func (k *keyStore) archivedPrivs(userID string) [][]byte {
	entries, err := os.ReadDir(k.dir)
	if err != nil {
		return nil
	}
	sum := sha256.Sum256([]byte(userID))
	prefix := hex.EncodeToString(sum[:]) + ".arch."
	var out [][]byte
	for _, e := range entries {
		if e.IsDir() || !hasPrefixSuffix(e.Name(), prefix, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(k.dir, e.Name()))
		if err != nil {
			continue
		}
		var rec storedKey
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		if priv, err := k.open(rec.PrivateNonce, rec.PrivateCT); err == nil {
			out = append(out, priv)
		}
	}
	return out
}

func hasPrefixSuffix(s, prefix, suffix string) bool {
	return len(s) >= len(prefix)+len(suffix) &&
		s[:len(prefix)] == prefix && s[len(s)-len(suffix):] == suffix
}
