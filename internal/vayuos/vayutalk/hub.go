// SPDX-License-Identifier: Apache-2.0

package vayutalk

import (
	"errors"
	"sync"
)

// Stream caps (FROZEN).
const (
	// MaxStreamsPerUser bounds concurrent live streams for one user. A new
	// stream past this evicts the user's oldest rather than being rejected, so a
	// client whose connection keeps dropping always reconnects successfully
	// instead of spiralling into "stream limit" errors (a stale stream can
	// linger until the proxy times out its upstream side). Room for app + web +
	// a spare device before any eviction.
	MaxStreamsPerUser = 5
	// MaxGlobalStreams bounds concurrent live streams across all users.
	MaxGlobalStreams = 500
	// subscriberBuffer is the per-subscriber channel depth. A stream that fills
	// it is closed rather than left open and deaf (see sendLocked): its client
	// reconnects, and the reconnect re-delivers every store-mode envelope still
	// queued. Live-mode envelopes have no queue, so a stuck stream loses those.
	subscriberBuffer = 64
)

// Subscribe outcomes.
var (
	// ErrUserStreamLimit is returned when a user already holds MaxStreamsPerUser.
	ErrUserStreamLimit = errors.New("vayutalk: per-user stream limit reached")
	// ErrGlobalStreamLimit is returned when the global stream cap is reached.
	ErrGlobalStreamLimit = errors.New("vayutalk: global stream limit reached")
)

// Event is one Server-Sent Event delivered to a live subscriber. Type is the
// SSE event name ("envelope" | "receipt"); Payload marshals to the data JSON.
type Event struct {
	Type    string
	Payload interface{}
}

// EnvelopePayload is the wire shape of an envelope event.
type EnvelopePayload struct {
	ID          string `json:"id"`
	From        string `json:"from"`
	Ciphertext  string `json:"ciphertext"` // base64 of opaque bytes
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at"`
	BurnSeconds int    `json:"burn_seconds"` // burn-after-read timer
	Mode        string `json:"mode"`
}

// ReceiptPayload is the wire shape of a receipt event.
type ReceiptPayload struct {
	ID     string `json:"id"`
	Status string `json:"status"` // "read" | "expired"
}

// PeerKeyPayload tells a subscriber that a peer's public key has become
// available locally — a late over-Tor fetch completing, typically. It carries the
// peer address and nothing else: no message content, no verification verdict. The
// client re-checks its own already-received messages.
type PeerKeyPayload struct {
	Peer string `json:"peer"`
}

type subscriber struct {
	ch  chan Event
	seq uint64 // monotonic insertion order, so the oldest can be evicted first
}

// Hub manages live subscribers keyed by user, enforcing the per-user and global
// stream caps. It is safe for concurrent use.
type Hub struct {
	mu    sync.Mutex
	subs  map[string]map[*subscriber]struct{}
	total int
	seq   uint64
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[*subscriber]struct{})}
}

// Subscribe registers a live stream for user, returning the event channel and a
// cancel func the caller MUST invoke on disconnect. Caps are enforced.
func (h *Hub) Subscribe(user string) (<-chan Event, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total >= MaxGlobalStreams {
		return nil, nil, ErrGlobalStreamLimit
	}
	// Evict this user's oldest stream(s) instead of rejecting the new one, so a
	// reconnect always succeeds. Closing the evicted subscriber's channel makes
	// its handler return (it reads `evt, ok := <-ch` and stops when ok is false).
	for len(h.subs[user]) >= MaxStreamsPerUser {
		h.evictOldestLocked(user)
	}
	h.seq++
	sub := &subscriber{ch: make(chan Event, subscriberBuffer), seq: h.seq}
	if h.subs[user] == nil {
		h.subs[user] = make(map[*subscriber]struct{})
	}
	h.subs[user][sub] = struct{}{}
	h.total++
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if set, ok := h.subs[user]; ok {
				if _, present := set[sub]; present {
					delete(set, sub)
					h.total--
					if len(set) == 0 {
						delete(h.subs, user)
					}
					close(sub.ch)
				}
			}
		})
	}
	return sub.ch, cancel, nil
}

// evictOldestLocked removes and closes the oldest (lowest-seq) subscriber for
// user. Closing the channel signals that subscriber's stream handler to return.
// The caller holds h.mu; the evicted subscriber's own cancel becomes a no-op
// because it checks membership before closing, so there is no double close.
func (h *Hub) evictOldestLocked(user string) {
	var oldest *subscriber
	for s := range h.subs[user] {
		if oldest == nil || s.seq < oldest.seq {
			oldest = s
		}
	}
	if oldest != nil {
		h.removeLocked(user, oldest)
	}
}

// removeLocked drops and closes one subscriber. Caller holds h.mu.
func (h *Hub) removeLocked(user string, sub *subscriber) {
	set := h.subs[user]
	if _, ok := set[sub]; !ok {
		return
	}
	delete(set, sub)
	h.total--
	if len(set) == 0 {
		delete(h.subs, user)
	}
	close(sub.ch)
}

// sendLocked hands evt to one subscriber without blocking. A subscriber whose
// buffer is full is closed instead of skipped: skipping left the stream open and
// silent, so a message never reached it and nothing told the client to
// reconnect and collect what was queued. Caller holds h.mu.
func (h *Hub) sendLocked(user string, sub *subscriber, evt Event) bool {
	select {
	case sub.ch <- evt:
		return true
	default:
		h.removeLocked(user, sub)
		return false
	}
}

// Publish delivers env to a live subscriber of env.To, returning true if at
// least one subscriber received it now. Delivery is non-blocking: a subscriber
// with a full buffer is closed (see sendLocked).
func (h *Hub) Publish(env *Envelope) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.subs[env.To]
	if len(set) == 0 {
		return false
	}
	evt := Event{Type: "envelope", Payload: envelopePayload(env)}
	delivered := false
	for sub := range set {
		if h.sendLocked(env.To, sub, evt) {
			delivered = true
		}
	}
	return delivered
}

// PublishReceipt fans a receipt out to every live subscriber of user (the
// original sender), non-blocking.
func (h *Hub) PublishReceipt(user, id, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.subs[user]
	if len(set) == 0 {
		return
	}
	evt := Event{Type: "receipt", Payload: ReceiptPayload{ID: id, Status: status}}
	for sub := range set {
		h.sendLocked(user, sub, evt)
	}
}

// PublishPeerKey tells one identity's own streams that a peer's key has arrived
// locally, so messages that could not be verified when they were received can be
// re-checked without a reload (ADR-0142). Addressed to the identity that owns the
// stream, because the reader is who needs to re-verify.
func (h *Hub) PublishPeerKey(user, peer string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.subs[user]
	if len(set) == 0 {
		return
	}
	evt := Event{Type: "peerkey", Payload: PeerKeyPayload{Peer: peer}}
	for sub := range set {
		h.sendLocked(user, sub, evt)
	}
}

// Online reports whether user currently holds at least one live stream.
func (h *Hub) Online(user string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[user]) > 0
}

// envelopePayload projects an Envelope onto its wire form.
func envelopePayload(env *Envelope) EnvelopePayload {
	return EnvelopePayload{
		ID:          env.ID,
		From:        env.From,
		Ciphertext:  encodeCiphertext(env.Ciphertext),
		CreatedAt:   env.CreatedAt.UTC().Format(timeLayout),
		ExpiresAt:   env.ExpiresAt.UTC().Format(timeLayout),
		BurnSeconds: env.BurnSeconds,
		Mode:        env.Mode,
	}
}
