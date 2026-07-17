// Package vayutalk implements the VayuTalk ephemeral, end-to-end-encrypted
// messaging relay: a bounded in-memory store of opaque ciphertext envelopes, a
// live SSE fan-out hub, and short-lived bearer tokens. The server never sees
// plaintext, never persists anything to SQLite, and a process restart purges
// every envelope. See docs/adr/ADR-0131-vayutalk-ephemeral-messaging.md.
package vayutalk

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// Protocol caps (FROZEN — the client relies on exactly these bounds).
const (
	// MaxCiphertextBytes is the largest decoded envelope payload accepted.
	MaxCiphertextBytes = 65536
	// MaxPerRecipientQueue bounds store-mode envelopes queued for one user.
	MaxPerRecipientQueue = 50
	// MaxGlobalQueue bounds store-mode envelopes across all users.
	MaxGlobalQueue = 5000
	// MinBurnSeconds / MaxBurnSeconds clamp the caller-supplied self-destruct
	// timer — how long a message survives AFTER the recipient first reads it
	// (burn-after-read). The client renders the countdown from this value.
	MinBurnSeconds = 5
	MaxBurnSeconds = 3600
	// DefaultBurnSeconds is used when the caller supplies no timer (5 minutes —
	// safe but usable).
	DefaultBurnSeconds = 300
)

// UnreadTTLSeconds bounds how long an UNREAD store-mode envelope is held in RAM
// before it is purged (the recipient never came to read it). It decouples the
// server's holding window from the per-message burn-after-read timer, so a short
// "5s after read" timer does not cause an unread message to vanish in 5s. It is
// a var (not const) so operators can tune it via VAYUOS_TALK_UNREAD_TTL_SECONDS;
// the default is 24h. Clamped to [5m, 7d] by SetUnreadTTL.
var UnreadTTLSeconds = 86400

// SetUnreadTTL sets the unread holding window, clamped to a sane [5m, 7d] range.
// Values ≤ 0 are ignored (keep the current setting).
func SetUnreadTTL(secs int) {
	if secs <= 0 {
		return
	}
	switch {
	case secs < 300:
		secs = 300
	case secs > 604800:
		secs = 604800
	}
	UnreadTTLSeconds = secs
}

// Enqueue outcomes.
var (
	// ErrRecipientQueueFull is returned when a recipient's queue is at its cap.
	ErrRecipientQueueFull = errors.New("vayutalk: recipient queue full")
	// ErrGlobalQueueFull is returned when the global queue is at its cap.
	ErrGlobalQueueFull = errors.New("vayutalk: global queue full")
	// ErrCiphertextTooLarge is returned for an oversized payload.
	ErrCiphertextTooLarge = errors.New("vayutalk: ciphertext too large")
)

// Envelope is one opaque, end-to-end-encrypted message in transit. The server
// stores ONLY the ciphertext and minimal routing metadata, in memory only.
type Envelope struct {
	ID          string
	From        string
	To          string
	Ciphertext  []byte
	CreatedAt   time.Time
	ExpiresAt   time.Time // server holding deadline (unread cap); GC only
	BurnSeconds int       // burn-after-read timer, delivered to clients
	Mode        string    // "live" | "store"
}

// ExpiredReceipt names an envelope that a purge removed and the sender that
// should be told it expired (if still streaming).
type ExpiredReceipt struct {
	Sender string
	ID     string
}

// Store is the bounded in-memory envelope store. All state lives in RAM; a
// restart purges everything. It is safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	byUser map[string][]*Envelope // recipient -> ordered pending list
	byID   map[string]*Envelope   // id -> envelope
	total  int
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{
		byUser: make(map[string][]*Envelope),
		byID:   make(map[string]*Envelope),
	}
}

// ClampBurn bounds a caller-supplied burn-after-read timer to
// [MinBurnSeconds, MaxBurnSeconds]. A value ≤ 0 (unset) becomes DefaultBurnSeconds.
func ClampBurn(secs int) int {
	switch {
	case secs <= 0:
		return DefaultBurnSeconds
	case secs < MinBurnSeconds:
		return MinBurnSeconds
	case secs > MaxBurnSeconds:
		return MaxBurnSeconds
	default:
		return secs
	}
}

// randID returns a cryptographically-random 32-byte base64url identifier.
func randID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read never returns an error on supported platforms; if it
		// somehow does, a time-seeded fallback is unacceptable for an ID, so we
		// surface an empty string and let the caller reject the operation.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewEnvelope mints an envelope with a fresh random ID. burnSeconds is the
// clamped burn-after-read timer carried to the clients; ExpiresAt is the server
// holding deadline (the unread cap), which is independent of the burn timer so a
// short "burn 5s after read" does not evict an unread message in 5 seconds. An
// oversized ciphertext is rejected before it is stored.
func NewEnvelope(from, to string, ciphertext []byte, burnSeconds int, mode string, now time.Time) (*Envelope, error) {
	if len(ciphertext) > MaxCiphertextBytes {
		return nil, ErrCiphertextTooLarge
	}
	id := randID()
	if id == "" {
		return nil, errors.New("vayutalk: could not generate id")
	}
	return &Envelope{
		ID:          id,
		From:        from,
		To:          to,
		Ciphertext:  ciphertext,
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Duration(UnreadTTLSeconds) * time.Second),
		BurnSeconds: ClampBurn(burnSeconds),
		Mode:        mode,
	}, nil
}

// Enqueue stores a store-mode envelope for later delivery, enforcing the
// per-recipient and global queue caps. It returns a cap error when full.
func (s *Store) Enqueue(env *Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.total >= MaxGlobalQueue {
		return ErrGlobalQueueFull
	}
	if len(s.byUser[env.To]) >= MaxPerRecipientQueue {
		return ErrRecipientQueueFull
	}
	s.byUser[env.To] = append(s.byUser[env.To], env)
	s.byID[env.ID] = env
	s.total++
	return nil
}

// Queued returns a snapshot of the envelopes pending for user, in arrival
// order, WITHOUT removing them. A store-mode envelope lives in the store until
// it is read-destroyed (ack) or purged (expiry), so a reconnecting client
// re-receives anything it never acknowledged (at-least-once delivery).
func (s *Store) Queued(user string) []*Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.byUser[user]
	if len(pending) == 0 {
		return nil
	}
	out := make([]*Envelope, len(pending))
	copy(out, pending)
	return out
}

// Delete removes an envelope by id (read-destruction). It returns the sender
// address and whether the envelope existed, so the caller can emit a read
// receipt to a streaming sender.
func (s *Store) Delete(id string) (sender string, existed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	env, ok := s.byID[id]
	if !ok {
		return "", false
	}
	s.removeLocked(env)
	return env.From, true
}

// PurgeExpired removes every envelope whose expiry is at or before now and
// returns a receipt per removed envelope so senders can be notified.
func (s *Store) PurgeExpired(now time.Time) []ExpiredReceipt {
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired []ExpiredReceipt
	for id, env := range s.byID {
		if !env.ExpiresAt.After(now) {
			expired = append(expired, ExpiredReceipt{Sender: env.From, ID: id})
			s.removeLocked(env)
		}
	}
	return expired
}

// Len reports the total number of queued envelopes (for diagnostics/tests).
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

// removeLocked deletes env from both indexes; caller holds s.mu.
func (s *Store) removeLocked(env *Envelope) {
	list := s.byUser[env.To]
	for i, e := range list {
		if e.ID == env.ID {
			s.byUser[env.To] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(s.byUser[env.To]) == 0 {
		delete(s.byUser, env.To)
	}
	delete(s.byID, env.ID)
	s.total--
}
