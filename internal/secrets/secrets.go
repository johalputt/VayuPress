// Package secrets provides an encrypted-at-rest store for third-party service
// credentials managed from the VayuOS admin panel — for example the IndexNow
// submission key, an n8n automation webhook token, a local Ollama endpoint, or
// an OpenRouter API key.
//
// Unlike VayuPress's own API keys (which are one-way hashed — see
// internal/apikeys), these secrets must be recoverable in plaintext at runtime
// so VayuPress can present them to the downstream service. They are therefore
// sealed with AES-256-GCM.
//
// Envelope encryption / automatic rotation
// ----------------------------------------
// Credentials are NOT encrypted directly with the API key. Instead a random,
// persistent Data Encryption Key (DEK) — stored once in the secret_keyring
// table — encrypts every credential. This deliberately decouples the at-rest
// encryption from any authentication credential, so rotating the VayuPress API
// key (or any issued key) never makes a stored secret undecryptable: nothing
// has to be re-entered. Rotation is therefore 100% automated.
//
// The DEK itself is held directly in the keyring when no dedicated encryption
// secret is configured (the default — fully self-managing), or wrapped by a Key
// Encryption Key (KEK) derived from VAYU_SECRET when one is set, for
// defence-in-depth. Because only the small DEK is wrapped, the encryption
// secret can be changed in place via RewrapMaster without touching — or losing
// — a single stored credential.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"context"
)

// Provider identifies the downstream service a credential targets. Known
// providers get first-class UI affordances; "custom" covers anything else.
const (
	ProviderIndexNow   = "indexnow"
	ProviderN8N        = "n8n"
	ProviderOllama     = "ollama"
	ProviderOpenRouter = "openrouter"
	ProviderCustom     = "custom"
)

// KnownProviders is the allowlist of provider slugs accepted on write.
var KnownProviders = map[string]bool{
	ProviderIndexNow:   true,
	ProviderN8N:        true,
	ProviderOllama:     true,
	ProviderOpenRouter: true,
	ProviderCustom:     true,
}

// kekCheckPlain is sealed under the KEK so a wrong/changed VAYU_SECRET is
// detected at boot instead of producing garbage decryptions.
const kekCheckPlain = "vayusecrets-kek-check-v1"

// ErrNotFound is returned when no credential matches the supplied id/provider.
var ErrNotFound = errors.New("secrets: credential not found")

// Credential is the metadata view of a stored secret. The secret plaintext is
// never carried on this type — only a masked hint for display.
type Credential struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Label     string    `json:"label"`
	Endpoint  string    `json:"endpoint"` // optional, stored in clear (e.g. base URL)
	Hint      string    `json:"hint"`     // masked secret, safe to display
	Enabled   bool      `json:"enabled"`
	HasSecret bool      `json:"has_secret"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store seals/unseals service credentials in the service_credentials table
// using a persistent DEK loaded from (or created in) the secret_keyring table.
type Store struct {
	db *sql.DB

	kekSecret []byte // optional; derives the KEK that wraps the DEK at rest

	once    sync.Once
	dek     [32]byte
	initErr error
}

// New creates a Store backed by db. kekSecret is the optional encryption secret
// (e.g. VAYU_SECRET) used to wrap the DEK at rest; pass nil/empty to let the
// store self-manage the DEK (default). The keyring is initialised lazily on
// first use.
func New(db *sql.DB, kekSecret []byte) *Store {
	return &Store{db: db, kekSecret: kekSecret}
}

// deriveKEK turns an encryption secret into a 32-byte AES key (domain
// separated, so it is distinct from any other use of the same secret).
func deriveKEK(secret []byte) [32]byte {
	return sha256.Sum256(append([]byte("vayusecrets-kek-v1\x00"), secret...))
}

// sealWith encrypts plaintext under key, returning "nonceHex.ctHex".
func sealWith(key [32]byte, plaintext []byte) (string, error) {
	block, err := aes.NewCipher(key[:])
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

// openWith decrypts a "nonceHex.ctHex" blob produced by sealWith.
func openWith(key [32]byte, blob string) ([]byte, error) {
	parts := strings.SplitN(blob, ".", 2)
	if len(parts) != 2 {
		return nil, errors.New("secrets: malformed sealed blob")
	}
	nonce, err := hex.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	ct, err := hex.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("secrets: bad nonce length")
	}
	return gcm.Open(nil, nonce, ct, nil)
}

// ensure lazily loads or creates the keyring DEK exactly once.
func (s *Store) ensure() error {
	s.once.Do(func() { s.initErr = s.loadOrCreateKeyring() })
	return s.initErr
}

func (s *Store) loadOrCreateKeyring() error {
	var dekField, kekSrc, kekCheck string
	err := s.db.QueryRow(`SELECT dek, kek_src, kek_check FROM secret_keyring WHERE id=1`).
		Scan(&dekField, &kekSrc, &kekCheck)
	hasKEK := len(s.kekSecret) > 0

	if errors.Is(err, sql.ErrNoRows) {
		// First boot: mint a fresh random DEK and persist it.
		if _, err := io.ReadFull(rand.Reader, s.dek[:]); err != nil {
			return err
		}
		if hasKEK {
			kek := deriveKEK(s.kekSecret)
			wrapped, err := sealWith(kek, s.dek[:])
			if err != nil {
				return err
			}
			check, err := sealWith(kek, []byte(kekCheckPlain))
			if err != nil {
				return err
			}
			_, err = s.db.Exec(`INSERT INTO secret_keyring(id, dek, kek_src, kek_check) VALUES(1,?,?,?)`, wrapped, "env", check)
			return err
		}
		_, err = s.db.Exec(`INSERT INTO secret_keyring(id, dek, kek_src, kek_check) VALUES(1,?,?,?)`, hex.EncodeToString(s.dek[:]), "none", "")
		return err
	}
	if err != nil {
		return err
	}

	// Existing keyring.
	switch kekSrc {
	case "env":
		if !hasKEK {
			return errors.New("secrets: stored credentials are protected by VAYU_SECRET, which is not set")
		}
		kek := deriveKEK(s.kekSecret)
		if _, err := openWith(kek, kekCheck); err != nil {
			return errors.New("secrets: VAYU_SECRET does not match the stored encryption key")
		}
		dek, err := openWith(kek, dekField)
		if err != nil {
			return err
		}
		copy(s.dek[:], dek)
		return nil
	default: // "none"
		raw, err := hex.DecodeString(dekField)
		if err != nil {
			return err
		}
		copy(s.dek[:], raw)
		return nil
	}
}

// RewrapMaster re-wraps the DEK under a new encryption secret without
// re-encrypting (or losing) any stored credential. Pass nil/empty to store the
// DEK directly. Safe to call at runtime; the in-memory DEK is unchanged.
func (s *Store) RewrapMaster(newKEKSecret []byte) error {
	if err := s.ensure(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if len(newKEKSecret) > 0 {
		kek := deriveKEK(newKEKSecret)
		wrapped, err := sealWith(kek, s.dek[:])
		if err != nil {
			return err
		}
		check, err := sealWith(kek, []byte(kekCheckPlain))
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE secret_keyring SET dek=?, kek_src='env', kek_check=?, rotated_at=? WHERE id=1`, wrapped, check, now); err != nil {
			return err
		}
	} else {
		if _, err := s.db.Exec(`UPDATE secret_keyring SET dek=?, kek_src='none', kek_check='', rotated_at=? WHERE id=1`, hex.EncodeToString(s.dek[:]), now); err != nil {
			return err
		}
	}
	s.kekSecret = newKEKSecret
	return nil
}

// seal/open encrypt/decrypt a credential secret with the active DEK, returning
// the separate nonce/ciphertext hex columns persisted per credential.
func (s *Store) seal(plaintext []byte) (nonceHex, ctHex string, err error) {
	blob, err := sealWith(s.dek, plaintext)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(blob, ".", 2)
	return parts[0], parts[1], nil
}

func (s *Store) open(nonceHex, ctHex string) ([]byte, error) {
	if nonceHex == "" && ctHex == "" {
		return nil, nil
	}
	return openWith(s.dek, nonceHex+"."+ctHex)
}

// mask renders a non-reversible hint for a secret: the last 4 chars revealed,
// the rest replaced with bullets. Empty input yields an empty hint.
func mask(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) <= 4 {
		return strings.Repeat("•", len(secret))
	}
	return strings.Repeat("•", 8) + secret[len(secret)-4:]
}

// stableID derives an opaque id from provider+label so the same logical
// credential upserts in place rather than duplicating.
func stableID(provider, label string) string {
	sum := sha256.Sum256([]byte(provider + "\x00" + label))
	return hex.EncodeToString(sum[:12])
}

// Upsert creates or updates a credential. When secret is empty on an update the
// existing sealed secret is preserved (so saving metadata doesn't wipe the key);
// pass clearSecret=true to explicitly remove it.
func (s *Store) Upsert(ctx context.Context, provider, label, endpoint, secret string, enabled, clearSecret bool) (string, error) {
	if err := s.ensure(); err != nil {
		return "", err
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if !KnownProviders[provider] {
		return "", errors.New("secrets: unknown provider")
	}
	label = strings.TrimSpace(label)
	if label == "" {
		// Fall back to a capitalised provider slug (ASCII, no deprecated APIs).
		if provider != "" {
			label = strings.ToUpper(provider[:1]) + provider[1:]
		} else {
			label = "Credential"
		}
	}
	id := stableID(provider, label)
	now := time.Now().UTC()

	var nonceHex, ctHex, hint string
	switch {
	case clearSecret:
		// leave nonce/ct empty, hint empty
	case strings.TrimSpace(secret) != "":
		var err error
		nonceHex, ctHex, err = s.seal([]byte(secret))
		if err != nil {
			return "", err
		}
		hint = mask(secret)
	default:
		// Preserve any existing secret/hint for this id.
		var en, ct, h string
		err := s.db.QueryRowContext(ctx,
			`SELECT secret_nonce, secret_ct, hint FROM service_credentials WHERE id=?`, id,
		).Scan(&en, &ct, &h)
		if err == nil {
			nonceHex, ctHex, hint = en, ct, h
		}
	}

	en := 0
	if enabled {
		en = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO service_credentials(id, provider, label, endpoint, secret_nonce, secret_ct, hint, enabled, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   endpoint=excluded.endpoint,
		   secret_nonce=excluded.secret_nonce,
		   secret_ct=excluded.secret_ct,
		   hint=excluded.hint,
		   enabled=excluded.enabled,
		   updated_at=excluded.updated_at`,
		id, provider, label, strings.TrimSpace(endpoint), nonceHex, ctHex, hint, en, now, now,
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

// SetEnabled toggles a credential's enabled flag.
func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool) error {
	en := 0
	if enabled {
		en = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE service_credentials SET enabled=?, updated_at=? WHERE id=?`,
		en, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a credential.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM service_credentials WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns all stored credentials (metadata + masked hint), newest first.
func (s *Store) List(ctx context.Context) ([]Credential, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, provider, label, endpoint, hint, enabled, secret_ct, created_at, updated_at
		 FROM service_credentials ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		var c Credential
		var enabled int
		var ct string
		if err := rows.Scan(&c.ID, &c.Provider, &c.Label, &c.Endpoint, &c.Hint, &enabled, &ct, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Enabled = enabled != 0
		c.HasSecret = ct != ""
		out = append(out, c)
	}
	return out, rows.Err()
}

// Reveal returns the decrypted secret for a single credential. Intended for an
// explicit admin "reveal" action; callers must already be authenticated.
func (s *Store) Reveal(ctx context.Context, id string) (string, error) {
	if err := s.ensure(); err != nil {
		return "", err
	}
	var nonceHex, ctHex string
	err := s.db.QueryRowContext(ctx,
		`SELECT secret_nonce, secret_ct FROM service_credentials WHERE id=?`, id,
	).Scan(&nonceHex, &ctHex)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	pt, err := s.open(nonceHex, ctHex)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// ProviderSecret returns the decrypted secret and endpoint of the first
// enabled credential for a provider, or empty strings if none is configured.
// This is the runtime accessor used by features such as IndexNow.
func (s *Store) ProviderSecret(ctx context.Context, provider string) (secret, endpoint string) {
	if err := s.ensure(); err != nil {
		return "", ""
	}
	var nonceHex, ctHex, ep string
	err := s.db.QueryRowContext(ctx,
		`SELECT secret_nonce, secret_ct, endpoint FROM service_credentials
		 WHERE provider=? AND enabled=1 ORDER BY updated_at DESC LIMIT 1`, provider,
	).Scan(&nonceHex, &ctHex, &ep)
	if err != nil {
		return "", ""
	}
	pt, err := s.open(nonceHex, ctHex)
	if err != nil {
		return "", ep
	}
	return string(pt), ep
}
