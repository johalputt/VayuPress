// Package preview provides signed draft preview tokens for VayuPress.
// A time-limited HMAC-signed token allows sharing a draft article URL with
// reviewers without requiring admin access or exposing the full API key.
package preview

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const defaultTTL = 48 * time.Hour

// Token holds the parsed contents of a preview token.
type Token struct {
	Slug      string
	ExpiresAt time.Time
}

// Signer creates and verifies draft preview tokens.
type Signer struct{ secret []byte }

// New creates a Signer. secret should be the site's VAYU_SECRET or similar.
func New(secret string) *Signer {
	if secret == "" {
		// Fallback to a random ephemeral secret (tokens won't survive restarts).
		b := make([]byte, 32)
		rand.Read(b)
		secret = hex.EncodeToString(b)
	}
	return &Signer{secret: []byte(secret)}
}

// Issue creates a new preview token for the given slug with the given TTL.
// A zero or negative TTL immediately expires the token.
func (s *Signer) Issue(slug string, ttl time.Duration) string {
	if ttl == 0 {
		ttl = defaultTTL
	}
	exp := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	payload := slug + ":" + exp
	mac := s.sign(payload)
	// Format: base64(slug:exp:mac)
	raw := payload + ":" + mac
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// Verify parses and validates a preview token.
// Returns ErrExpired when the token is past its TTL, ErrInvalid for bad tokens.
func (s *Signer) Verify(token string) (*Token, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("%w: bad encoding", ErrInvalid)
	}
	parts := strings.SplitN(string(raw), ":", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: bad format", ErrInvalid)
	}
	slug, expStr, mac := parts[0], parts[1], parts[2]
	// Verify HMAC.
	payload := slug + ":" + expStr
	expected := s.sign(payload)
	if !hmac.Equal([]byte(mac), []byte(expected)) {
		return nil, fmt.Errorf("%w: signature mismatch", ErrInvalid)
	}
	// Check expiry.
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: bad expiry", ErrInvalid)
	}
	exp := time.Unix(expUnix, 0)
	if time.Now().After(exp) {
		return nil, ErrExpired
	}
	return &Token{Slug: slug, ExpiresAt: exp}, nil
}

func (s *Signer) sign(payload string) string {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

// ErrExpired is returned when a preview token has passed its TTL.
var ErrExpired = fmt.Errorf("preview token expired")

// ErrInvalid is returned when a preview token cannot be verified.
var ErrInvalid = fmt.Errorf("preview token invalid")
