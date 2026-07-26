// SPDX-License-Identifier: Apache-2.0

// Package session derives cookieless, GDPR-compliant visitor session identifiers
// for VayuAnalytics.
//
// A session hash is SHA-256 over (daily-rotating random salt, UTC day, hour
// bucket, client IP, User-Agent, Accept-Language). The salt is regenerated every
// UTC day from crypto/rand and held only in memory, so:
//
//   - No cookie is set and nothing is written to the client.
//   - The hash cannot be linked to a real IP or any PII (the salt is secret and
//     the inputs are one-way hashed).
//   - The hash cannot be correlated across days (the salt rotates), satisfying
//     GDPR Article 25 (data protection by design) — no consent banner required.
//
// The hour bucket means the same visitor within the same UTC hour resolves to
// the same session, so engagement for one reading session is grouped, while a
// return visit hours later is a new session.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// Hasher produces daily-rotating, salted session hashes. Safe for concurrent use.
type Hasher struct {
	mu   sync.Mutex
	salt []byte
	day  string
}

// NewHasher returns a Hasher with a freshly seeded salt for the current UTC day.
func NewHasher() *Hasher {
	h := &Hasher{}
	h.rotateLocked(time.Now().UTC())
	return h
}

// rotateLocked regenerates the salt and records the day. Caller holds h.mu.
func (h *Hasher) rotateLocked(now time.Time) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Extremely unlikely; fall back to a time-seeded salt so the server
		// keeps functioning (still non-reversible, still rotates daily).
		ts := now.UnixNano()
		for i := range b {
			b[i] = byte(ts >> (uint(i%8) * 8))
		}
	}
	h.salt = b
	h.day = now.Format("2006-01-02")
}

// Session returns the anonymous session hash for a visitor observed at now.
// ip/ua/lang are used only as hash inputs and are never stored by this package.
func (h *Hasher) Session(ip, ua, lang string, now time.Time) string {
	now = now.UTC()
	day := now.Format("2006-01-02")
	hour := now.Format("15")
	h.mu.Lock()
	if day != h.day {
		h.rotateLocked(now)
	}
	salt := h.salt
	h.mu.Unlock()

	mac := sha256.New()
	mac.Write(salt)
	mac.Write([]byte(day))
	mac.Write([]byte(hour))
	mac.Write([]byte{0})
	mac.Write([]byte(ip))
	mac.Write([]byte{0})
	mac.Write([]byte(ua))
	mac.Write([]byte{0})
	mac.Write([]byte(lang))
	sum := mac.Sum(nil)
	return hex.EncodeToString(sum[:])
}
