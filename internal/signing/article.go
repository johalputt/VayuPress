// Package signing provides Ed25519 tamper-proof signing for VayuPress articles.
// A signature covers the canonical JSON of (id, title, body, author_id, published_at).
package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// ArticlePayload is the canonical form that gets signed.
type ArticlePayload struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	AuthorID    string `json:"author_id"`
	PublishedAt string `json:"published_at"`
}

// SignedArticle wraps a payload with its Ed25519 signature.
type SignedArticle struct {
	Payload   ArticlePayload `json:"payload"`
	Signature string         `json:"signature"`  // base64-std-encoded
	PublicKey string         `json:"public_key"` // base64-std-encoded
}

// GenerateKeyPair creates a new Ed25519 key pair.
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// Sign creates a SignedArticle using the given private key.
func Sign(priv ed25519.PrivateKey, p ArticlePayload) (*SignedArticle, error) {
	msg, err := canonicalJSON(p)
	if err != nil {
		return nil, fmt.Errorf("signing: marshal: %w", err)
	}
	sig := ed25519.Sign(priv, msg)
	pub := priv.Public().(ed25519.PublicKey)
	return &SignedArticle{
		Payload:   p,
		Signature: base64.StdEncoding.EncodeToString(sig),
		PublicKey: base64.StdEncoding.EncodeToString(pub),
	}, nil
}

// Verify checks that the article's signature is valid.
func Verify(sa *SignedArticle) error {
	pub, err := base64.StdEncoding.DecodeString(sa.PublicKey)
	if err != nil {
		return fmt.Errorf("signing: decode pubkey: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(sa.Signature)
	if err != nil {
		return fmt.Errorf("signing: decode sig: %w", err)
	}
	msg, err := canonicalJSON(sa.Payload)
	if err != nil {
		return fmt.Errorf("signing: marshal: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), msg, sig) {
		return errors.New("signing: invalid signature")
	}
	return nil
}

func canonicalJSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}
