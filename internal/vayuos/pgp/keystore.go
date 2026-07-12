package pgp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
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

// keyStore persists encrypted keypairs to a directory and maintains in-memory
// indexes for lookup. Filenames are NOT a bare hash of the (email-bearing)
// userID — that would let anyone with directory access confirm whether a given
// address has a key by recomputing the hash. Instead every file is located
// through indexes rebuilt from file *contents* at startup, so a filename need
// carry no recoverable link to its owner; new files are named with a keyed
// HMAC (opaque without the secret). Legacy files written under the old bare-hash
// scheme still load unchanged, because lookup no longer depends on the name.
type keyStore struct {
	dir     string
	aeadKey [32]byte // AES-256-GCM key for private-key ciphertext (unchanged scheme)
	nameKey [32]byte // HMAC key for deriving new filenames (domain-separated)

	mu       sync.RWMutex
	byEmail  map[string]string   // email  → userID
	fileByID map[string]string   // userID → key file base name
	archByID map[string][]string // userID → archive file base names
}

// newKeyStore opens (and lazily creates) a key store under dir, deriving the
// at-rest AES key from masterSecret. The derived keys are held only in memory.
func newKeyStore(dir string, masterSecret []byte) (*keyStore, error) {
	if dir == "" {
		return nil, errors.New("vayupgp: empty storage dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("vayupgp: create storage dir: %w", err)
	}
	ks := &keyStore{
		dir:      dir,
		byEmail:  make(map[string]string),
		fileByID: make(map[string]string),
		archByID: make(map[string][]string),
	}
	// Domain-separated derivation so the keystore key is distinct from any other
	// use of the master secret. Never log or persist the derived key. This scheme
	// is FROZEN — changing it would make every stored private key undecryptable.
	ks.aeadKey = sha256.Sum256(append([]byte("vayupgp-keystore-v1\x00"), masterSecret...))
	// The filename HMAC key is domain-separated from the AEAD key.
	nk := hmac.New(sha256.New, ks.aeadKey[:])
	nk.Write([]byte("vayupgp-keystore-filename-v1"))
	copy(ks.nameKey[:], nk.Sum(nil))
	if err := ks.reindex(); err != nil {
		return nil, err
	}
	return ks, nil
}

// nameHash derives an opaque, non-reversible file token for a userID using a
// keyed HMAC (not a bare hash), so a filename cannot be correlated back to the
// email it belongs to without the master secret.
func (k *keyStore) nameHash(userID string) string {
	mac := hmac.New(sha256.New, k.nameKey[:])
	mac.Write([]byte(userID))
	return hex.EncodeToString(mac.Sum(nil))
}

// keyFileName returns the base name a NEW key file for userID is written under.
// Existing files are located through fileByID (built from contents), so their
// on-disk names — whatever scheme wrote them — keep working unchanged.
func (k *keyStore) keyFileName(userID string) string {
	return k.nameHash(userID) + ".key.json"
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
	// Reuse the existing file name for an update (preserving a legacy-named file
	// in place); a brand-new key gets a fresh HMAC name.
	k.mu.RLock()
	base := k.fileByID[rec.UserID]
	k.mu.RUnlock()
	if base == "" {
		base = k.keyFileName(rec.UserID)
	}
	dst := filepath.Join(k.dir, base)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	k.mu.Lock()
	k.byEmail[normalizeEmail(rec.Email)] = rec.UserID
	k.fileByID[rec.UserID] = base
	k.mu.Unlock()
	return nil
}

// readRecord reads and unmarshals one stored record by its indexed file name.
func (k *keyStore) readRecord(userID string) (storedKey, error) {
	k.mu.RLock()
	base := k.fileByID[userID]
	k.mu.RUnlock()
	if base == "" {
		return storedKey{}, ErrNotFound
	}
	data, err := os.ReadFile(filepath.Join(k.dir, base))
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

// load reads a record and returns it plus the decrypted armored private key.
func (k *keyStore) load(userID string) (storedKey, []byte, error) {
	rec, err := k.readRecord(userID)
	if err != nil {
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
	return k.readRecord(userID)
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

// reindex rebuilds every in-memory index from disk CONTENTS (not from file
// names), so a key or archive is found by the userID recorded inside it — which
// is why the on-disk name never has to encode a recoverable link to its owner.
func (k *keyStore) reindex() error {
	entries, err := os.ReadDir(k.dir)
	if err != nil {
		return err
	}
	byEmail := make(map[string]string)
	fileByID := make(map[string]string)
	archByID := make(map[string][]string)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		isArchive := strings.Contains(name, ".arch.")
		isKey := strings.HasSuffix(name, ".key.json")
		if !isArchive && !isKey {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(k.dir, name))
		if rerr != nil {
			continue
		}
		var rec storedKey
		if json.Unmarshal(data, &rec) != nil || rec.UserID == "" {
			continue
		}
		if isArchive {
			archByID[rec.UserID] = append(archByID[rec.UserID], name)
			continue
		}
		byEmail[normalizeEmail(rec.Email)] = rec.UserID
		fileByID[rec.UserID] = name
	}
	k.mu.Lock()
	k.byEmail, k.fileByID, k.archByID = byEmail, fileByID, archByID
	k.mu.Unlock()
	return nil
}

// archive stores a rotated-out private key so historical ciphertext encrypted
// to it stays decryptable. New archives use the keyed-HMAC name scheme; older
// ones remain findable through archByID (indexed from contents at startup).
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
	base := k.nameHash(rec.UserID) + ".arch." + rec.Fingerprint + ".json"
	if err := os.WriteFile(filepath.Join(k.dir, base), data, 0o600); err != nil {
		return err
	}
	k.mu.Lock()
	k.archByID[rec.UserID] = append(k.archByID[rec.UserID], base)
	k.mu.Unlock()
	return nil
}

// archivedPrivs returns the decrypted armored private keys archived for userID.
func (k *keyStore) archivedPrivs(userID string) [][]byte {
	k.mu.RLock()
	names := append([]string(nil), k.archByID[userID]...)
	k.mu.RUnlock()
	var out [][]byte
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(k.dir, name))
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
