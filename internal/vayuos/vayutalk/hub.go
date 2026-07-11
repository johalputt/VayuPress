package vayutalk

import (
	"errors"
	"sync"
)

// Stream caps (FROZEN).
const (
	// MaxStreamsPerUser bounds concurrent live streams for one user.
	MaxStreamsPerUser = 3
	// MaxGlobalStreams bounds concurrent live streams across all users.
	MaxGlobalStreams = 500
	// subscriberBuffer is the per-subscriber channel depth. A slow client that
	// fills it drops further live events rather than blocking the publisher; the
	// store-mode queue is the durable path for anything that must not be missed.
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
	ID         string `json:"id"`
	From       string `json:"from"`
	Ciphertext string `json:"ciphertext"` // base64 of opaque bytes
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
	Mode       string `json:"mode"`
}

// ReceiptPayload is the wire shape of a receipt event.
type ReceiptPayload struct {
	ID     string `json:"id"`
	Status string `json:"status"` // "read" | "expired"
}

type subscriber struct {
	ch chan Event
}

// Hub manages live subscribers keyed by user, enforcing the per-user and global
// stream caps. It is safe for concurrent use.
type Hub struct {
	mu    sync.Mutex
	subs  map[string]map[*subscriber]struct{}
	total int
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
	if len(h.subs[user]) >= MaxStreamsPerUser {
		return nil, nil, ErrUserStreamLimit
	}
	sub := &subscriber{ch: make(chan Event, subscriberBuffer)}
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

// Publish delivers env to a live subscriber of env.To, returning true if at
// least one subscriber received it now. Delivery is non-blocking: a full
// subscriber buffer is skipped (store-mode queueing is the durable path).
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
		select {
		case sub.ch <- evt:
			delivered = true
		default:
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
		select {
		case sub.ch <- evt:
		default:
		}
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
		ID:         env.ID,
		From:       env.From,
		Ciphertext: encodeCiphertext(env.Ciphertext),
		CreatedAt:  env.CreatedAt.UTC().Format(timeLayout),
		ExpiresAt:  env.ExpiresAt.UTC().Format(timeLayout),
		Mode:       env.Mode,
	}
}
