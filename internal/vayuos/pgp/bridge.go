package pgp

import (
	"net/http"
	"time"
)

// PGPUser is the minimal identity VayuPGP needs to mint a keypair.
type PGPUser struct {
	UserID string
	Name   string
	Email  string
}

// Keypair is the public-facing description of a stored OpenPGP keypair. It never
// carries private key material in plaintext.
type Keypair struct {
	UserID      string    `json:"user_id"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	Fingerprint string    `json:"fingerprint"`
	PublicArmor string    `json:"public_armor"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Revoked     bool      `json:"revoked"`
}

// PublicKey is a recipient's public key, located either locally or via WKD.
type PublicKey struct {
	Email       string `json:"email"`
	Fingerprint string `json:"fingerprint"`
	Armor       string `json:"armor"`
	Source      string `json:"source"` // "local" or "wkd"
}

// KeyStatus summarises a key's lifecycle state for the VayuOS panel.
type KeyStatus struct {
	UserID          string    `json:"user_id"`
	Email           string    `json:"email"`
	Fingerprint     string    `json:"fingerprint"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	Revoked         bool      `json:"revoked"`
	Expired         bool      `json:"expired"`
	ExpiringSoon    bool      `json:"expiring_soon"`
	DaysUntilExpiry int       `json:"days_until_expiry"`
}

// Bridge is the only contract between VayuPress core and VayuPGP. Core code
// depends on this interface, never on the concrete engine.
type Bridge interface {
	// Key lifecycle.
	GenerateKeypair(user *PGPUser) (*Keypair, error)
	GetKeypair(userID string) (*Keypair, error)
	GetPublicKey(email string) (*PublicKey, error)
	RevokeKeypair(userID string) error
	RotateKeypair(userID string) (*Keypair, error)
	ListExpiringKeys(within time.Duration) ([]*Keypair, error)

	// Crypto operations.
	Encrypt(plaintext []byte, recipientEmail string) ([]byte, error)
	Decrypt(ciphertext []byte, userID string) ([]byte, error)
	Sign(data []byte, userID string) ([]byte, error)
	Verify(data, sig []byte, senderEmail string) (bool, error)
	EncryptAndSign(plaintext []byte, recipientEmail, senderUserID string) ([]byte, error)

	// WKD (RFC draft / Web Key Directory).
	ServeWKD(domain string) http.Handler
	LookupExternalKey(email string) (*PublicKey, error)

	// Import / export.
	ExportPublicKey(userID string) ([]byte, error)
	ImportPublicKey(armored []byte) (*PublicKey, error)

	// Status.
	GetKeyStatus(userID string) (*KeyStatus, error)
	ListKeys() ([]*Keypair, error)
}

// compile-time assurance the engine satisfies the bridge.
var _ Bridge = (*Engine)(nil)
