// SPDX-License-Identifier: Apache-2.0

// Package gossip carries VayuShield verdicts between nodes of a multi-node
// install.
//
// # The gap it closes
//
// The challenge signer is already derived from the shared secret, so a visitor
// who solves a proof of work on one node walks onto another unchallenged with no
// code at all. Nothing else is shared. Reputation, the jail, learned signatures
// and the kernel banlist are per-process memory, so a swarm hitting five nodes
// gets five independent free rides, and every deploy resets an attacker's
// escalation. This package moves the verdicts, and only the verdicts.
//
// # The key is DERIVED, never the shared secret itself
//
// API_KEY also guards the MCP server and the REST API. Handing it raw to N edge
// nodes would mean one compromised edge compromises all three, everywhere. The
// gossip key is HKDF-SHA256 over that secret with a fixed salt and info string,
// so a node holding it can authenticate verdicts and cannot recover the secret,
// mint an API token, or speak to the MCP server.
//
// # Sealed, not merely signed
//
// Messages carry visitor IP addresses. Authenticating them and leaving them
// readable would put the audience of a product that ships a Tor Space onto the
// wire in cleartext for anyone between two nodes, so the payload is sealed with
// AES-256-GCM under the derived key rather than signed with an HMAC. The cost is
// identical and the posture is not.
//
// # What an attacker gets from a compromised node, and what bounds it
//
// A compromised edge holds the derived key and can therefore issue verdicts.
// That is inherent — any node that can jail a source locally can jail one
// remotely — so the design bounds the blast radius rather than pretending to
// prevent it:
//
//   - A verdict can never do more than that node could do locally. It carries no
//     TTL of its own; the receiver applies its OWN jail duration.
//   - The operator's allow list still wins. A network an operator declared
//     trusted cannot be jailed by a peer, which is also what keeps a compromised
//     node from locking the operator out of their own fleet.
//   - Accepted verdicts are rate-limited per origin node, so a compromised one
//     cannot enumerate the internet into the fleet's jails.
//   - Messages are bounded in size and count on both the sending and receiving
//     side, so a peer cannot make the receiver expensive.
//   - Freshness is bounded in BOTH directions and nonces are remembered, so a
//     captured message cannot be replayed — and a future-dated one cannot be
//     minted to replay forever.
//
// # There is deliberately no forwarding
//
// A Message has no hop count and no TTL field, and that absence is the design.
// Verdicts are pushed directly to configured peers and never relayed. A relaying
// gossip mesh is an amplifier: one message in, N out, and a loop in the peer
// graph is a self-inflicted flood on the exact machines that are already under
// attack. N nodes is a small number an operator configures; a full mesh of
// direct pushes is O(N) messages and needs no cleverness to stay safe.
package gossip

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/hkdf"
)

// Limits. Every one of these is enforced on BOTH sides: a sender that would
// exceed one is a bug caught in testing, and a receiver that trusted the sender
// to have checked would be trusting a party that may be compromised.
const (
	// MaxVerdicts per message. A batch is for a burst of local decisions, not a
	// bulk transfer of someone's blocklist.
	MaxVerdicts = 256
	// MaxMessageBytes caps a sealed message. Well above a full batch and far
	// below anything that makes decryption a denial of service.
	MaxMessageBytes = 64 << 10
	// MaxAge is how old a message may be. Verdicts are perishable — a jail
	// decision from an hour ago says nothing useful now — so the window is short,
	// which also keeps the replay cache small.
	MaxAge = 60 * time.Second
	// MaxSkew is how far into the future a message may be dated. Without this,
	// one message dated years ahead stays "fresh" forever and is replayable for
	// as long as it is held.
	MaxSkew = 10 * time.Second
	// nonceLen is AES-GCM's standard nonce size.
	nonceLen = 12
)

var (
	ErrNoSecret     = errors.New("gossip: no shared secret; clustering is off")
	ErrTooLarge     = errors.New("gossip: message exceeds the size limit")
	ErrTooMany      = errors.New("gossip: too many verdicts in one message")
	ErrNotAuthentic = errors.New("gossip: message failed authentication")
	ErrStale        = errors.New("gossip: message is outside the freshness window")
	ErrReplay       = errors.New("gossip: message nonce has already been seen")
	ErrNoNode       = errors.New("gossip: message names no origin node")
)

// Kind is what a verdict asks the receiver to do.
type Kind uint8

const (
	// kindUnset is the zero value and is not a valid kind. A message that omits
	// the field must be rejected rather than silently meaning whatever the first
	// constant happens to be — the same discipline as the enforcement-rule
	// registry, and for the same reason.
	kindUnset Kind = iota
	// KindJail — this source was jailed on the origin node.
	KindJail
	// KindSuspect — this source lost reputation without being jailed. Weaker,
	// and the common case: it lets a swarm's escalation accumulate across the
	// fleet instead of restarting on each node.
	KindSuspect
	// KindPardon — this source proved itself (solved a challenge, verified) and
	// should be released. Carried so a false positive heals fleet-wide rather
	// than only where the visitor happened to land.
	KindPardon
)

// Valid reports whether a kind was actually set to something meaningful.
func (k Kind) Valid() bool { return k == KindJail || k == KindSuspect || k == KindPardon }

// Verdict is one decision about one source.
//
// It carries no duration on purpose. A sending node does not get to choose how
// long a receiver punishes someone: the receiver applies its own configured jail
// TTL, so a compromised node cannot issue a thousand-year sentence, and an
// operator's own settings remain the only thing that decides how long anything
// lasts on their machine.
type Verdict struct {
	Kind   Kind   `json:"k"`
	Source string `json:"s"`
	// Weight is the reputation delta for KindSuspect, clamped by the receiver.
	Weight float64 `json:"w,omitempty"`
}

// Message is one batch from one node.
type Message struct {
	// Node identifies the origin, for per-peer accounting and rate limiting. It
	// is authenticated (it is inside the sealed payload) but NOT trusted: it
	// names which key-holder spoke, and every key-holder is equally able to lie
	// about everything else.
	Node string `json:"n"`
	// Issued is unix seconds. Sealed as additional data as well as carried in the
	// payload, so it cannot be edited to extend a captured message's life.
	Issued int64 `json:"t"`
	// Nonce is the GCM nonce, hex-encoded, filled in by Open for use as the
	// replay-cache key. It is deliberately NOT marshalled: GCM binds the nonce
	// into the tag, so the value on the wire is already authenticated — changing
	// one byte of it fails Open — and carrying a second copy inside the payload
	// bought nothing. It also could not survive the trip: random bytes in a Go
	// string are not valid UTF-8, and encoding/json silently rewrites those to
	// U+FFFD, so the echoed copy never matched the original.
	Nonce    string    `json:"-"`
	Verdicts []Verdict `json:"v"`
}

// DeriveKey produces the gossip key from the install's shared secret.
//
// HKDF with a fixed salt and info string, so the result is deterministic across
// nodes (they must agree without talking) and one-way (a node holding it cannot
// recover API_KEY and therefore cannot mint an API token or speak to the MCP
// server with it). An empty secret is an error rather than a key derived from
// nothing: clustering that silently ran under a well-known key would be worse
// than clustering that refused to start.
func DeriveKey(secret string) ([32]byte, error) {
	var k [32]byte
	if secret == "" {
		return k, ErrNoSecret
	}
	r := hkdf.New(sha256.New, []byte(secret),
		[]byte("vayushield/gossip/v1"), []byte("verdict-seal"))
	if _, err := io.ReadFull(r, k[:]); err != nil {
		return k, err
	}
	return k, nil
}

// Seal encrypts and authenticates a message.
func Seal(key [32]byte, m Message) ([]byte, error) {
	if m.Node == "" {
		return nil, ErrNoNode
	}
	if len(m.Verdicts) > MaxVerdicts {
		return nil, ErrTooMany
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	pt, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	g, err := aead(key)
	if err != nil {
		return nil, err
	}
	// The timestamp is additional data as well as payload. Editing the on-wire
	// timestamp to extend a captured message's life therefore breaks the tag.
	out := g.Seal(append([]byte{}, nonce...), nonce, pt, aad(m.Issued))
	if len(out) > MaxMessageBytes {
		return nil, ErrTooLarge
	}
	return out, nil
}

// Open authenticates, decrypts and validates a message.
//
// It takes the timestamp from the caller rather than reading the clock, so the
// freshness window is testable, and it rejects in a fixed order: size, then
// authentication, then freshness. Anything that would let an unauthenticated
// party cause work — parsing, allocating, touching the replay cache — happens
// only after the tag has verified.
func Open(key [32]byte, b []byte, issuedAt int64, now time.Time) (Message, error) {
	var m Message
	if len(b) > MaxMessageBytes || len(b) < nonceLen+16 {
		return m, ErrTooLarge
	}
	g, err := aead(key)
	if err != nil {
		return m, err
	}
	nonce := b[:nonceLen]
	pt, err := g.Open(nil, nonce, b[nonceLen:], aad(issuedAt))
	if err != nil {
		return m, ErrNotAuthentic
	}
	if err := json.Unmarshal(pt, &m); err != nil {
		return m, ErrNotAuthentic
	}
	// The sealed timestamp must be the one that was authenticated. A mismatch
	// means the caller was handed a different value than the sender bound in.
	if m.Issued != issuedAt {
		return m, ErrNotAuthentic
	}
	if m.Node == "" {
		return m, ErrNoNode
	}
	// The wire nonce is authenticated by the tag that just verified, so it is a
	// safe replay key.
	m.Nonce = hex.EncodeToString(nonce)
	if len(m.Verdicts) > MaxVerdicts {
		return m, ErrTooMany
	}

	age := now.Sub(time.Unix(m.Issued, 0))
	if age > MaxAge || age < -MaxSkew {
		return m, ErrStale
	}
	// Drop verdicts that name no source or no valid kind rather than failing the
	// whole batch: one malformed entry from a peer must not discard the other
	// 255, and the zero Kind is deliberately not a valid answer.
	kept := m.Verdicts[:0]
	for _, v := range m.Verdicts {
		if v.Source != "" && v.Kind.Valid() {
			kept = append(kept, v)
		}
	}
	m.Verdicts = kept
	return m, nil
}

func aead(key [32]byte) (cipher.AEAD, error) {
	c, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(c)
}

func aad(issued int64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(issued))
	return b[:]
}

// ── Replay defence ────────────────────────────────────────────────────────────

// Seen is a bounded replay cache.
//
// Fixed memory is the requirement, not an optimisation: the entries are keyed by
// a value a peer chooses, so a map that grew with traffic would be a map an
// attacker sizes. Entries older than MaxAge+MaxSkew cannot pass the freshness
// check anyway, so eviction by age is both correct and sufficient.
type Seen struct {
	mu   sync.Mutex
	m    map[string]int64 // nonce -> unix seconds first seen
	cap  int
	last int64
}

// NewSeen returns a replay cache holding at most cap nonces.
func NewSeen(capacity int) *Seen {
	if capacity <= 0 {
		capacity = 8192
	}
	return &Seen{m: make(map[string]int64, 64), cap: capacity}
}

// Fresh records a nonce and reports whether it had NOT been seen before. It is
// the atomic check-and-claim, so two concurrent deliveries of the same replayed
// message cannot both be accepted.
func (s *Seen) Fresh(nonce string, now time.Time) bool {
	if s == nil || nonce == "" {
		return false
	}
	sec := now.Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	if sec != s.last {
		s.last = sec
		s.evictLocked(sec)
	}
	if _, dup := s.m[nonce]; dup {
		return false
	}
	if len(s.m) >= s.cap {
		s.evictLocked(sec)
		if len(s.m) >= s.cap {
			// Still full of live entries. Refuse rather than evict something that
			// is still within its window: dropping a live nonce would re-open the
			// replay it exists to close, and refusing only costs a peer one
			// message it will re-send.
			return false
		}
	}
	s.m[nonce] = sec
	return true
}

func (s *Seen) evictLocked(now int64) {
	cutoff := now - int64((MaxAge+MaxSkew)/time.Second) - 1
	for k, t := range s.m {
		if t < cutoff {
			delete(s.m, k)
		}
	}
}

// Len reports how many nonces are held, for tests and the panel.
func (s *Seen) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}
