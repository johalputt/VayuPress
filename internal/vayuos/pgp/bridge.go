// Package pgp provides the VayuPGP subsystem — end-to-end PGP encryption
// based on ProtonMail go-crypto (Apache 2.0).
//
// VayuPGP auto-generates keypairs for every user, auto-signs outgoing mail,
// auto-encrypts when recipient keys are known via WKD, and serves a WKD
// keyserver at /.well-known/openpgpkey/.
//
// Key algorithm: Ed25519 primary (sign+cert) + Curve25519 subkey (encrypt).
// Keys expire after 2 years and auto-rotate 30 days before expiry.
package pgp

import (
	"encoding/json"
	"net/http"
	"time"
)

// ── Core types ───────────────────────────────────────────────────────────────

// PGPUser holds the info needed to generate a keypair.
type PGPUser struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

// Keypair is a generated PGP keypair.
type Keypair struct {
	UserID         string    `json:"user_id"`
	Fingerprint    string    `json:"fingerprint"`
	PublicKey      []byte    `json:"public_key"`
	PublicKeyArmor string    `json:"public_key_armor"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	Algorithm      string    `json:"algorithm"`
	// PrivateKey is encrypted at rest and never serialized in JSON.
	PrivateKeyEncrypted []byte `json:"-"`
}

// PublicKey holds a published public key.
type PublicKey struct {
	Fingerprint string    `json:"fingerprint"`
	Email       string    `json:"email"`
	Armor       string    `json:"armor"`
	UserID      string    `json:"user_id"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// KeyStatus reports the health of a user's keypair.
type KeyStatus struct {
	HasKey      bool      `json:"has_key"`
	Fingerprint string    `json:"fingerprint"`
	ExpiresAt   time.Time `json:"expires_at"`
	DaysLeft    int       `json:"days_left"`
	Expiring    bool      `json:"expiring"`
	Algorithm   string    `json:"algorithm"`
}

// ── Config ───────────────────────────────────────────────────────────────────

// Config holds VayuPGP configuration.
type Config struct {
	Enabled             bool   `json:"enabled"`
	AutoGenerate        bool   `json:"auto_generate"`
	AutoEncrypt         bool   `json:"auto_encrypt"`
	AutoSign            bool   `json:"auto_sign"`
	KeyAlgorithm        string `json:"key_algorithm"`
	KeyExpiryDays       int    `json:"key_expiry_days"`
	RotationNoticeDays  int    `json:"rotation_notice_days"`
	WKDEnabled          bool   `json:"wkd_enabled"`
	MasterSecret        string `json:"-"` // never serialized
}

// DefaultConfig returns a Config with safe defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:            true,
		AutoGenerate:       true,
		AutoEncrypt:        true,
		AutoSign:           true,
		KeyAlgorithm:       "ed25519",
		KeyExpiryDays:      730, // 2 years
		RotationNoticeDays: 30,
		WKDEnabled:         true,
	}
}

// ── Bridge interface ─────────────────────────────────────────────────────────

// Bridge is the ONLY way VayuPress core talks to VayuPGP and vice versa.
type Bridge interface {
	// Key lifecycle
	GenerateKeypair(user *PGPUser) (*Keypair, error)
	GetKeypair(userID string) (*Keypair, error)
	GetPublicKey(email string) (*PublicKey, error)
	RevokeKeypair(userID string) error
	RotateKeypair(userID string) (*Keypair, error)
	ListExpiringKeys(within time.Duration) ([]*Keypair, error)

	// Crypto operations
	Encrypt(plaintext []byte, recipientEmail string) ([]byte, error)
	Decrypt(ciphertext []byte, userID string) ([]byte, error)
	Sign(data []byte, userID string) ([]byte, error)
	Verify(data, sig []byte, senderEmail string) (bool, error)
	EncryptAndSign(plaintext []byte, recipientEmail string, senderUserID string) ([]byte, error)

	// WKD (Web Key Directory) — RFC 9579
	ServeWKD(domain string) http.Handler
	PublishKey(key *PublicKey) error
	LookupExternalKey(email string) (*PublicKey, error)

	// Import/Export
	ExportPublicKey(userID string) ([]byte, error)
	ExportPrivateKey(userID string, passphrase []byte) ([]byte, error)
	ImportPublicKey(armored []byte) (*PublicKey, error)

	// Status
	GetKeyStatus(userID string) (*KeyStatus, error)
}

// ── JSON helpers ─────────────────────────────────────────────────────────────

// WKDResponse is the RFC 9579 direct lookup response.
// See https://datatracker.ietf.org/doc/html/rfc9579
type WKDResponse struct {
	Keys []json.RawMessage `json:"keys"`
}