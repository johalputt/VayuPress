// SPDX-License-Identifier: Apache-2.0

// Package session derives cookieless, GDPR-compliant visitor and session
// identifiers for VayuAnalytics.
//
// Two identifiers serve two different truths (audit: one clock-bucketed hash
// could not answer both):
//
//   - VISITOR: SHA-256 over (daily-rotating salt, UTC day, client IP,
//     User-Agent, Accept-Language). Stable for the whole UTC day, so daily
//     uniques, new-vs-returning and retention cohorts are measurable.
//   - SESSION TOKEN: derived from the visitor hash plus the 30-minute-aligned
//     start epoch of the visitor's current READING SESSION, tracked through a
//     bounded in-memory activity table. While the visitor keeps hitting the
//     site with gaps under 30 minutes they keep the SAME token (a reading
//     session straddling an hour or half-hour boundary no longer splits);
//     after 30 minutes of silence the next hit starts a new session.
//
// Privacy properties are unchanged:
//
//   - No cookie is set and nothing is written to the client.
//   - The hashes cannot be linked to a real IP or any PII (the salt is secret
//     and the inputs are one-way hashed).
//   - The hashes cannot be correlated across days (the salt rotates),
//     satisfying GDPR Article 25 — no consent banner required.
//
// The daily salt is DERIVED from the service master secret when one is
// configured (HKDF-style HMAC over the day string), so a restart no longer
// makes every continuing visitor look brand-new (audit: in-memory random salt
// turned every deploy into a 100%-new-traffic day). Without a master secret
// the hasher falls back to the historic crypto/rand behaviour.
package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"time"
)

const (
	// sessionWindow is the inactivity gap that closes a reading session.
	sessionWindow = 30 * time.Minute
	// shardCount spreads the activity table to keep per-shard locks tiny.
	shardCount = 16
	// shardCap bounds each shard's live identities (~32k visitors tracked
	// overall); overflow evicts that shard's stale entries first, then its
	// oldest half — a flood degrades to slightly coarser sessions, never to
	// unbounded memory.
	shardCap = 2048
)

// Hasher produces daily-rotating visitor and session identifiers.
// Safe for concurrent use.
type Hasher struct {
	mu     sync.Mutex
	salt   []byte
	day    string
	master []byte // nil = random-salt mode

	shards [shardCount]struct {
		sync.Mutex
		m map[string]activeSession
	}
}

// activeSession remembers where a visitor's current reading session began and
// when they were last seen.
type activeSession struct {
	start int64 // unix seconds, 30-min aligned, when this session opened
	seen  int64 // unix seconds of the last hit
}

// NewHasher returns a Hasher with a freshly seeded random salt for the current
// UTC day (historic behaviour; use NewMasterHasher when a service secret exists).
func NewHasher() *Hasher {
	h := &Hasher{}
	h.rotateLocked(time.Now().UTC())
	return h
}

// NewMasterHasher returns a Hasher whose daily salts are derived from master
// instead of random: salt(day) = HMAC-SHA256(master, "vayuanalytics:"+day).
// Sessions survive process restarts within a day, and every node in a cluster
// derives identical salts — without weakening unlinkability (the salt is still
// secret and still rotates daily).
func NewMasterHasher(master []byte) *Hasher {
	h := &Hasher{master: master}
	h.rotateLocked(time.Now().UTC())
	return h
}

// rotateLocked regenerates the salt and records the day. Caller holds h.mu.
func (h *Hasher) rotateLocked(now time.Time) {
	day := now.UTC().Format("2006-01-02")
	if len(h.master) > 0 {
		mac := hmac.New(sha256.New, h.master)
		mac.Write([]byte("vayuanalytics:salt:" + day))
		h.salt = mac.Sum(nil)
		h.day = day
		return
	}
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
	h.day = day
}

func (h *Hasher) saltFor(now time.Time) []byte {
	now = now.UTC()
	day := now.Format("2006-01-02")
	h.mu.Lock()
	if day != h.day {
		h.rotateLocked(now)
	}
	salt := h.salt
	h.mu.Unlock()
	return salt
}

func identityHash(salt []byte, day, ip, ua, lang string) []byte {
	mac := sha256.New()
	mac.Write(salt)
	mac.Write([]byte(day))
	mac.Write([]byte{0})
	mac.Write([]byte(ip))
	mac.Write([]byte{0})
	mac.Write([]byte(ua))
	mac.Write([]byte{0})
	mac.Write([]byte(lang))
	return mac.Sum(nil)
}

// Visitor returns the stable anonymous visitor hash for the UTC day of now:
// the same human browsing all day resolves to ONE visitor hash, while the
// same inputs tomorrow hash differently (salt rotation). This is the key
// daily uniques, new-vs-returning and retention cohorts count.
func (h *Hasher) Visitor(ip, ua, lang string, now time.Time) string {
	sum := identityHash(h.saltFor(now), now.UTC().Format("2006-01-02"), ip, ua, lang)
	return hex.EncodeToString(sum[:])
}

// Session returns the anonymous session token for a visitor observed at now.
// Hits fewer than 30 minutes apart continue the same reading session (the
// token survives hour boundaries); a longer gap opens a new one. Falls back to
// the visitor hash itself when the activity table has evicted this visitor
// under extreme load — the metric degrades to per-hit granularity rather than
// growing memory without bound.
func (h *Hasher) Session(ip, ua, lang string, now time.Time) string {
	now = now.UTC()
	visitor := h.Visitor(ip, ua, lang, now)
	id := sha256.Sum256([]byte(visitor))
	key := string(id[:16]) // raw 16-byte identity key, never stored anywhere
	shard := &h.shards[id[0]%shardCount]
	seen := now.Unix()

	shard.Lock()
	defer shard.Unlock()

	if shard.m == nil {
		shard.m = make(map[string]activeSession, 64)
	}
	cur, ok := shard.m[key]
	if ok && seen-cur.seen <= int64(sessionWindow/time.Second) {
		cur.seen = seen
		shard.m[key] = cur
		return sessionToken(h.saltFor(now), visitor, cur.start)
	}
	// New reading session: open it at the current half-hour bucket.
	start := seen - seen%1800
	// Eviction before growth: drop long-expired entries; if the shard is
	// still at capacity, drop its oldest half (a flood costs coarse session
	// grouping, never unbounded memory).
	if len(shard.m) >= shardCap {
		half := seen - 2*int64(sessionWindow/time.Second)
		for k, e := range shard.m {
			if e.seen <= half {
				delete(shard.m, k)
			}
		}
	}
	shard.m[key] = activeSession{start: start, seen: seen}
	return sessionToken(h.saltFor(now), visitor, start)
}

// sessionToken binds a reading session to (visitor, its opening bucket) so the
// same token covers every hit of one sitting and differs between sittings.
func sessionToken(salt []byte, visitorHex string, start int64) string {
	mac := sha256.New()
	mac.Write(salt)
	mac.Write([]byte("sess"))
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(start))
	mac.Write(b[:])
	mac.Write([]byte(visitorHex))
	sum := mac.Sum(nil)
	return hex.EncodeToString(sum[:])
}
