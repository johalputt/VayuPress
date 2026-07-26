// SPDX-License-Identifier: Apache-2.0

// Package challenge implements VayuShield's escalating challenge engine.
//
// It is fully sovereign and stateless: challenges and session tokens are HMAC
// -signed blobs the client echoes back, so no server-side challenge state is
// stored and the system scales horizontally with zero coordination. Nothing
// leaves the server and no third-party (Cloudflare Turnstile, hCaptcha, …) is
// contacted.
//
// The escalation ladder (decided by the middleware from the BotScore) is:
//
//	Level 1  Silent proof-of-work  — a SHA-256 PoW solved in a Web Worker (<100ms
//	         for a real browser; a JS-less/headless client fails or stalls).
//	Level 2  Interstitial JS challenge — a heavier PoW behind a branded page.
//	Level 3  Hard block            — 403 with a clean error page (logged).
//	Level 4  Tarpit                — accepted but deliberately delayed + garbled.
//
// This package owns the cryptographic core (PoW issue/verify + session token
// issue/verify). The HTTP surface (endpoints, interstitial HTML, tarpit) lives
// in the middleware/cmd layer so this package stays pure and unit-testable.
package challenge

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Signer issues and verifies HMAC-signed proof-of-work challenges and session
// tokens. Construct one per process with a stable secret (the site API key /
// master secret is a good source). Safe for concurrent use — it holds only an
// immutable key.
type Signer struct {
	key []byte
}

// NewSigner derives a challenge signer from a secret. The key is derived with
// HMAC (the secret is the HMAC key, never hashed as message data), which accepts
// any secret length, is domain-separated by a fixed label, and does not hold the
// raw secret verbatim — the appropriate construction for deriving a MAC key from
// a high-entropy application secret.
func NewSigner(secret []byte) *Signer {
	kdf := hmac.New(sha256.New, secret)
	kdf.Write([]byte("vayushield/challenge/v1"))
	return &Signer{key: kdf.Sum(nil)}
}

func (s *Signer) mac(msg []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write(msg)
	return m.Sum(nil)
}

// ── Proof of work ─────────────────────────────────────────────────────────────

// PoW is a proof-of-work challenge. The client must find a nonce such that
// hex(SHA-256(Salt + ":" + nonce)) begins with Difficulty '0' hex characters.
// Each hex zero is 4 bits of work, so Difficulty=4 ≈ 16 bits ≈ 65k hashes,
// comfortably under 100ms in a Web Worker yet punishing for a client that must
// spin up a JS engine per request.
type PoW struct {
	Salt       string `json:"salt"`
	Difficulty int    `json:"difficulty"`
	Expires    int64  `json:"exp"`
	Sig        string `json:"sig"`
}

// DefaultDifficulty is the Level-1 silent PoW difficulty (hex leading zeros).
const DefaultDifficulty = 4

// HardDifficulty is the Level-2 interstitial PoW difficulty.
const HardDifficulty = 5

const maxDifficulty = 8

// IssuePoW mints a signed PoW challenge valid for ttl. The signature binds the
// salt, difficulty and expiry so a client cannot lower the difficulty or replay
// an expired challenge.
func (s *Signer) IssuePoW(difficulty int, ttl time.Duration) (PoW, error) {
	if difficulty <= 0 {
		difficulty = DefaultDifficulty
	}
	if difficulty > maxDifficulty {
		difficulty = maxDifficulty
	}
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return PoW{}, err
	}
	p := PoW{
		Salt:       hex.EncodeToString(saltBytes),
		Difficulty: difficulty,
		Expires:    time.Now().Add(ttl).Unix(),
	}
	p.Sig = base64.RawURLEncoding.EncodeToString(s.mac(p.signingBytes()))
	return p, nil
}

func (p PoW) signingBytes() []byte {
	return []byte(p.Salt + "|" + strconv.Itoa(p.Difficulty) + "|" + strconv.FormatInt(p.Expires, 10))
}

// Errors returned by VerifyPoW.
var (
	ErrExpired      = errors.New("challenge expired")
	ErrBadSignature = errors.New("challenge signature invalid")
	ErrNotSolved    = errors.New("proof of work not satisfied")
)

// VerifyPoW checks that (challenge, nonce) is a valid, unexpired, correctly
// -signed challenge whose solution meets the required difficulty. It is
// constant-time on the signature comparison to avoid a signing-oracle.
func (s *Signer) VerifyPoW(p PoW, nonce string) error {
	expected := s.mac(p.signingBytes())
	got, err := base64.RawURLEncoding.DecodeString(p.Sig)
	if err != nil || !hmac.Equal(expected, got) {
		return ErrBadSignature
	}
	if time.Now().Unix() > p.Expires {
		return ErrExpired
	}
	if !SolutionValid(p.Salt, nonce, p.Difficulty) {
		return ErrNotSolved
	}
	return nil
}

// SolutionValid reports whether hex(SHA-256(salt:nonce)) has at least difficulty
// leading '0' hex characters. Exposed so the same predicate can be reused by
// tests and any server-side solver.
func SolutionValid(salt, nonce string, difficulty int) bool {
	if difficulty <= 0 {
		return true
	}
	sum := sha256.Sum256([]byte(salt + ":" + nonce))
	h := hex.EncodeToString(sum[:])
	if difficulty > len(h) {
		return false
	}
	for i := 0; i < difficulty; i++ {
		if h[i] != '0' {
			return false
		}
	}
	return true
}

// Solve finds a nonce satisfying the challenge, bounded by maxTries. It is the
// reference solver (also used to prove the JS solver's contract in tests). It
// returns the nonce and true, or "" and false if no solution was found in
// maxTries attempts.
func Solve(salt string, difficulty, maxTries int) (string, bool) {
	for i := 0; i < maxTries; i++ {
		n := strconv.Itoa(i)
		if SolutionValid(salt, n, difficulty) {
			return n, true
		}
	}
	return "", false
}

// ── Session tokens ────────────────────────────────────────────────────────────

// tokenVersion prefixes session tokens so the format can evolve.
const tokenVersion byte = 1

// IssueSession mints an opaque, signed session verification token valid for ttl,
// CRYPTOGRAPHICALLY BOUND to a client key (typically the client IP). The binding
// is folded into the MAC — it is NOT stored in the token — so the cookie carries
// only an issue timestamp and random entropy (no PII, no visitor identifier: a
// security-essential, not tracking, cookie under GDPR), yet a token minted for
// one client fails verification when presented from another. This defeats the
// "solve once, replay the cookie across a whole botnet" attack: a stolen or
// shared clearance cookie is worthless from a different source.
// Format: base64url(version || exp(8) || rand(8) || hmac(16)); mac = HMAC(payload || bind).
func (s *Signer) IssueSession(ttl time.Duration, bind string) (string, error) {
	payload := make([]byte, 1+8+8)
	payload[0] = tokenVersion
	binary.BigEndian.PutUint64(payload[1:9], uint64(time.Now().Add(ttl).Unix()))
	if _, err := rand.Read(payload[9:17]); err != nil {
		return "", err
	}
	mac := s.macBound(payload, bind)[:16]
	tok := append(payload, mac...)
	return base64.RawURLEncoding.EncodeToString(tok), nil
}

// macBound computes the session MAC over the payload folded with the client
// binding, so a token is only valid when presented with the same binding.
func (s *Signer) macBound(payload []byte, bind string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write(payload)
	m.Write([]byte{0}) // domain-separate payload from binding
	m.Write([]byte(bind))
	return m.Sum(nil)
}

// VerifySession reports whether tok is a valid, unexpired, correctly-signed
// session token issued for the given client binding. Constant-time on the MAC
// comparison. A token presented with a different binding than it was issued for
// fails — so a clearance cookie replayed from another source is rejected.
func (s *Signer) VerifySession(tok, bind string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(tok))
	if err != nil || len(raw) != 1+8+8+16 || raw[0] != tokenVersion {
		return false
	}
	payload := raw[:17]
	mac := raw[17:]
	expected := s.macBound(payload, bind)[:16]
	if !hmac.Equal(expected, mac) {
		return false
	}
	exp := int64(binary.BigEndian.Uint64(payload[1:9]))
	return time.Now().Unix() <= exp
}
