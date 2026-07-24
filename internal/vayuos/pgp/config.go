// Package pgp implements VayuPGP — VayuPress's native, end-to-end PGP privacy
// layer built on ProtonMail go-crypto (Apache-2.0).
//
// VayuPGP is a first-class subsystem of VayuPress (not a plugin and not a
// separate process). It generates and stores OpenPGP keypairs, performs
// encrypt/decrypt/sign/verify, serves a Web Key Directory (WKD), and rotates
// keys — all inside the single VayuPress binary.
//
// Security posture (VayuPress Constitution):
//   - Private keys are encrypted at rest with AES-256-GCM under a key derived
//     from the VayuPress master secret; the plaintext private key never touches
//     disk and is never logged.
//   - Key generation uses an Ed25519 primary (sign/certify) plus a Curve25519
//     (X25519/ECDH) encryption subkey, with a default 2-year validity.
package pgp

import "time"

// Config controls the VayuPGP engine.
type Config struct {
	// Enabled gates the whole subsystem.
	Enabled bool

	// AutoGenerate creates a keypair automatically when a user is created.
	AutoGenerate bool
	// AutoEncrypt encrypts outgoing mail when a recipient key is known.
	AutoEncrypt bool
	// AutoSign signs all outgoing mail with the sender's key.
	AutoSign bool

	// KeyExpiry is the validity period of newly generated keys.
	KeyExpiry time.Duration
	// RotationNotice is how long before expiry a rotation warning fires.
	RotationNotice time.Duration

	// WKDEnabled serves the Web Key Directory at /.well-known/openpgpkey/.
	WKDEnabled bool

	// StorageDir is the base directory for the encrypted key store.
	StorageDir string

	// MasterSecret is used ONLY to compute the legacy at-rest key so an existing
	// keystore migrates to envelope encryption with its keys still decryptable
	// (audit M8). It is no longer the ongoing at-rest key. Never persisted here.
	MasterSecret []byte

	// KEKSecret wraps the keystore's persistent DEK at rest via a dedicated
	// encryption secret (e.g. VAYU_SECRET), decoupling at-rest confidentiality
	// from the wire-exposed API key (audit M8). Optional.
	KEKSecret []byte
	// KEKFilePath is a host-bound 0600 file used to wrap the DEK when KEKSecret
	// is unset — so the wrapping key lives outside the ciphertext, not beside it.
	// Optional; when neither is set the DEK is stored unwrapped (with a warning).
	KEKFilePath string
}

// DefaultConfig returns the constitutional defaults: privacy on by default,
// Ed25519+Curve25519 keys valid for two years, WKD served.
func DefaultConfig() Config {
	return Config{
		Enabled:        true,
		AutoGenerate:   true,
		AutoEncrypt:    true,
		AutoSign:       true,
		KeyExpiry:      2 * 365 * 24 * time.Hour, // 2 years
		RotationNotice: 30 * 24 * time.Hour,      // 30 days
		WKDEnabled:     true,
		StorageDir:     "./vayudata/pgp",
	}
}
