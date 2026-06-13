// Package did provides Decentralized Identity (DID) authentication for VayuPress.
// Supports did:key method using Ed25519 key pairs.
// Challenge-response: server issues a nonce; client signs it with their DID key;
// server verifies the signature against the DID's public key.
package did

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// DIDDocument is a minimal DID Document for did:key.
type DIDDocument struct {
	ID         string    `json:"id"`          // e.g. "did:key:z6Mk..."
	Controller string    `json:"controller"`
	PublicKey  string    `json:"publicKeyBase64"` // base64-encoded Ed25519 public key
	Created    time.Time `json:"created"`
}

// Challenge is a server-issued nonce for authentication.
type Challenge struct {
	Nonce     string    `json:"nonce"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AuthRequest is sent by the client to authenticate.
type AuthRequest struct {
	DID       string `json:"did"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"` // base64 Ed25519 signature over nonce
}

// Authenticator handles DID-based challenge-response authentication.
type Authenticator struct {
	mu         sync.Mutex
	challenges map[string]*Challenge // key: nonce
	ttl        time.Duration
}

// NewAuthenticator creates an Authenticator with the given challenge TTL.
func NewAuthenticator(ttl time.Duration) *Authenticator {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Authenticator{
		challenges: make(map[string]*Challenge),
		ttl:        ttl,
	}
}

// IssueChallenge generates a new nonce for the client to sign.
func (a *Authenticator) IssueChallenge() (*Challenge, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("did: issue challenge: %w", err)
	}
	c := &Challenge{
		Nonce:     hex.EncodeToString(nonce),
		IssuedAt:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(a.ttl),
	}
	a.mu.Lock()
	a.challenges[c.Nonce] = c
	a.mu.Unlock()
	return c, nil
}

// Verify validates an AuthRequest against the issued challenge and the DID's public key.
// Returns the authenticated DID string on success.
func (a *Authenticator) Verify(doc *DIDDocument, req *AuthRequest) error {
	a.mu.Lock()
	c, ok := a.challenges[req.Nonce]
	if ok {
		delete(a.challenges, req.Nonce) // one-time use
	}
	a.mu.Unlock()

	if !ok {
		return errors.New("did: unknown or expired nonce")
	}
	if time.Now().After(c.ExpiresAt) {
		return errors.New("did: challenge expired")
	}
	if doc.ID != req.DID {
		return fmt.Errorf("did: DID mismatch: doc=%s req=%s", doc.ID, req.DID)
	}

	pubBytes, err := base64.StdEncoding.DecodeString(doc.PublicKey)
	if err != nil {
		return fmt.Errorf("did: decode public key: %w", err)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(req.Signature)
	if err != nil {
		return fmt.Errorf("did: decode signature: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), []byte(req.Nonce), sigBytes) {
		return errors.New("did: invalid signature")
	}
	return nil
}

// GenerateDIDKey creates a new did:key DID document and private key.
func GenerateDIDKey() (*DIDDocument, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	// did:key uses multibase-encoded multicodec — use hex prefix for simplicity
	id := "did:key:z" + strings.ToLower(hex.EncodeToString(pub))
	doc := &DIDDocument{
		ID:        id,
		Controller: id,
		PublicKey: pubB64,
		Created:   time.Now().UTC(),
	}
	return doc, priv, nil
}

// SignChallenge signs a nonce with the given private key.
func SignChallenge(priv ed25519.PrivateKey, nonce string) string {
	sig := ed25519.Sign(priv, []byte(nonce))
	return base64.StdEncoding.EncodeToString(sig)
}
