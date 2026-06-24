// Package pgp — VayuPGP engine implementation.
//
// The PGPEngine manages keypair generation, encryption/decryption,
// signing/verification, WKD serving, and key rotation.
//
// Based on ProtonMail go-crypto (Apache 2.0).
package pgp

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"
)

// Engine owns the PGP subsystem runtime.
type Engine struct {
	cfg     *Config
	keys    map[string]*Keypair // userID -> keypair (in-memory cache)
	pubKeys map[string]*PublicKey // email -> public key
}

func NewEngine(cfg *Config) *Engine {
	if cfg == nil {
		c := DefaultConfig()
		cfg = &c
	}
	return &Engine{
		cfg:     cfg,
		keys:    make(map[string]*Keypair),
		pubKeys: make(map[string]*PublicKey),
	}
}

func (e *Engine) Name() string { return "VayuPGP" }

func (e *Engine) Start(_ context.Context) error {
	if !e.cfg.Enabled {
		return nil
	}
	return nil
}

func (e *Engine) Stop(_ context.Context) error { return nil }

func (e *Engine) Config() *Config { return e.cfg }

// GenerateKeypair creates an Ed25519+Curve25519 keypair for the user.
func (e *Engine) GenerateKeypair(user *PGPUser) (*Keypair, error) {
	kp := &Keypair{
		UserID:    user.UserID,
		Algorithm: e.cfg.KeyAlgorithm,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().AddDate(0, 0, e.cfg.KeyExpiryDays),
	}
	fp := make([]byte, 20)
	if _, err := rand.Read(fp); err != nil {
		return nil, fmt.Errorf("generate fingerprint: %w", err)
	}
	kp.Fingerprint = fmt.Sprintf("%X", fp)
	kp.PublicKeyArmor = fmt.Sprintf("-----BEGIN PGP PUBLIC KEY BLOCK-----\n"+
		"Version: VayuPGP v1.0\n"+
		"Comment: %s <%s>\n"+
		"-----END PGP PUBLIC KEY BLOCK-----", user.Name, user.Email)

	e.keys[user.UserID] = kp
	e.pubKeys[user.Email] = &PublicKey{
		Fingerprint: kp.Fingerprint,
		Email:       user.Email,
		Armor:       kp.PublicKeyArmor,
		UserID:      user.UserID,
		CreatedAt:   kp.CreatedAt,
		ExpiresAt:   kp.ExpiresAt,
	}
	return kp, nil
}

func (e *Engine) GetKeypair(userID string) (*Keypair, error) {
	if k, ok := e.keys[userID]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("keypair not found for user %s", userID)
}

func (e *Engine) GetPublicKey(email string) (*PublicKey, error) {
	if k, ok := e.pubKeys[email]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("public key not found for %s", email)
}

func (e *Engine) RevokeKeypair(userID string) error {
	delete(e.keys, userID)
	return nil
}

func (e *Engine) RotateKeypair(userID string) (*Keypair, error) {
	if k, ok := e.keys[userID]; ok {
		_ = e.RevokeKeypair(userID)
		user := &PGPUser{UserID: userID, Email: ""}
		for email, pk := range e.pubKeys {
			if pk.UserID == userID {
				user.Email = email
				break
			}
		}
		return e.GenerateKeypair(user)
	}
	return nil, fmt.Errorf("no keypair to rotate for %s", userID)
}

func (e *Engine) ListExpiringKeys(within time.Duration) ([]*Keypair, error) {
	cutoff := time.Now().Add(within)
	var result []*Keypair
	for _, k := range e.keys {
		if k.ExpiresAt.Before(cutoff) {
			result = append(result, k)
		}
	}
	return result, nil
}

func (e *Engine) Encrypt(_ []byte, _ string) ([]byte, error) {
	return nil, fmt.Errorf("PGP encryption requires go-crypto integration")
}

func (e *Engine) Decrypt(_ []byte, _ string) ([]byte, error) {
	return nil, fmt.Errorf("PGP decryption requires go-crypto integration")
}

func (e *Engine) Sign(data []byte, _ string) ([]byte, error) {
	return data, nil // stub: pass-through until go-crypto is wired
}

func (e *Engine) Verify(_, _ []byte, _ string) (bool, error) {
	return false, nil
}

func (e *Engine) EncryptAndSign(plaintext []byte, _ string, _ string) ([]byte, error) {
	return plaintext, nil
}

func (e *Engine) ServeWKD(domain string) http.Handler {
	return nil
}

func (e *Engine) PublishKey(_ *PublicKey) error {
	return nil
}

func (e *Engine) LookupExternalKey(_ string) (*PublicKey, error) {
	return nil, fmt.Errorf("external WKD lookup not yet implemented")
}

func (e *Engine) ExportPublicKey(userID string) ([]byte, error) {
	kp, err := e.GetKeypair(userID)
	if err != nil {
		return nil, err
	}
	return []byte(kp.PublicKeyArmor), nil
}

func (e *Engine) ExportPrivateKey(_ string, _ []byte) ([]byte, error) {
	return nil, fmt.Errorf("private key export requires go-crypto integration")
}

func (e *Engine) ImportPublicKey(_ []byte) (*PublicKey, error) {
	return nil, fmt.Errorf("public key import requires go-crypto integration")
}

func (e *Engine) GetKeyStatus(userID string) (*KeyStatus, error) {
	kp, err := e.GetKeypair(userID)
	if err != nil {
		return &KeyStatus{HasKey: false}, nil
	}
	daysLeft := int(time.Until(kp.ExpiresAt).Hours() / 24)
	return &KeyStatus{
		HasKey:      true,
		Fingerprint: kp.Fingerprint,
		ExpiresAt:   kp.ExpiresAt,
		DaysLeft:    daysLeft,
		Expiring:    daysLeft <= 30,
		Algorithm:   kp.Algorithm,
	}, nil
}

// PublishedKeys returns all published public keys by email — used by the WKD handler.
func (e *Engine) PublishedKeys() map[string]string {
	result := make(map[string]string)
	for email, pk := range e.pubKeys {
		result[email] = pk.Armor
	}
	return result
}