// SPDX-License-Identifier: Apache-2.0

package vayutalk

import (
	"context"
	"encoding/base64"
	"errors"
	"time"
)

// timeLayout is the timestamp format used in envelope wire payloads.
const timeLayout = time.RFC3339

// ConnectTTLSeconds is the token lifetime reported to clients on /connect.
const ConnectTTLSeconds = int(TokenTTL / time.Second)

// purgeInterval is how often the single purge goroutine sweeps expired
// envelopes and tokens.
const purgeInterval = 2 * time.Second

// VerifyFunc authenticates a mailbox credential in mail-sync scope. It returns
// true only for an accepted credential (device approval enforced upstream).
type VerifyFunc func(ctx context.Context, email, password string) bool

// PubKeyFunc resolves a recipient's armored PGP public key and fingerprint.
type PubKeyFunc func(email string) (armored, fingerprint string, err error)

// Config injects the host dependencies so this package stays free of cmd/
// imports and of any direct PGP/DB coupling.
type Config struct {
	Enabled bool
	Verify  VerifyFunc
	PubKey  PubKeyFunc
}

// Engine is the VayuTalk subsystem: it owns the in-memory store, the live hub,
// the token table, and the single purge goroutine. It implements the VayuOS
// Subsystem contract (Name/Start) plus Stop.
type Engine struct {
	enabled bool
	verify  VerifyFunc
	pubkey  PubKeyFunc

	store  *Store
	hub    *Hub
	tokens *tokenStore

	done chan struct{}
}

// NewEngine constructs the engine from injected dependencies.
func NewEngine(cfg Config) *Engine {
	return &Engine{
		enabled: cfg.Enabled,
		verify:  cfg.Verify,
		pubkey:  cfg.PubKey,
		store:   NewStore(),
		hub:     NewHub(),
		tokens:  newTokenStore(),
		done:    make(chan struct{}),
	}
}

// Name identifies the subsystem for the boot orchestrator.
func (e *Engine) Name() string { return "VayuTalk" }

// Enabled reports whether VayuTalk is serving.
func (e *Engine) Enabled() bool { return e != nil && e.enabled }

// Start launches the single purge goroutine, bound to ctx. When disabled it is
// a no-op so the binary still boots.
func (e *Engine) Start(ctx context.Context) error {
	if !e.enabled {
		return errors.New("vayutalk: disabled")
	}
	if e.verify == nil || e.pubkey == nil {
		return errors.New("vayutalk: dependencies not injected")
	}
	go e.purgeLoop(ctx)
	return nil
}

// Stop halts the purge goroutine.
func (e *Engine) Stop(_ context.Context) error {
	select {
	case <-e.done:
	default:
		close(e.done)
	}
	return nil
}

// purgeLoop removes expired envelopes (emitting expired receipts to streaming
// senders) and prunes expired tokens every purgeInterval, until the engine
// stops or ctx is cancelled. Exactly one such goroutine exists per engine.
func (e *Engine) purgeLoop(ctx context.Context) {
	ticker := time.NewTicker(purgeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			for _, r := range e.store.PurgeExpired(now) {
				e.hub.PublishReceipt(r.Sender, r.ID, "expired")
			}
			e.tokens.sweep(now)
		}
	}
}

// Connect verifies a mailbox credential and, on success, mints a bearer token.
// The boolean is false on any failure so the caller returns a uniform 401.
func (e *Engine) Connect(ctx context.Context, email, password string) (string, bool) {
	if !e.enabled || e.verify == nil {
		return "", false
	}
	if !e.verify(ctx, email, password) {
		return "", false
	}
	token := e.tokens.mint(email, time.Now())
	if token == "" {
		return "", false
	}
	return token, true
}

// Authenticate resolves a bearer token to its mailbox address.
func (e *Engine) Authenticate(token string) (string, bool) {
	return e.tokens.lookup(token, time.Now())
}

// Subscribe opens a live stream for user and drains any queued store-mode
// envelopes for immediate flush. The queued envelopes are returned so the
// caller writes them to the stream before entering the live loop; the caller
// MUST invoke cancel on disconnect.
func (e *Engine) Subscribe(user string) (queued []*Envelope, ch <-chan Event, cancel func(), err error) {
	ch, cancel, err = e.hub.Subscribe(user)
	if err != nil {
		return nil, nil, nil, err
	}
	queued = e.store.Queued(user)
	return queued, ch, cancel, nil
}

// Send routes an envelope per its mode. live: deliver only if the recipient is
// online now, never queue. store: deliver live if online, else queue until read
// or the unread cap. burnSeconds is the burn-after-read timer carried to the
// clients. It returns the envelope id, whether it was delivered live now, and
// whether it was queued for later.
func (e *Engine) Send(from, to string, ciphertext []byte, burnSeconds int, mode string) (id string, delivered, queued bool, err error) {
	env, err := NewEnvelope(from, to, ciphertext, burnSeconds, mode, time.Now())
	if err != nil {
		return "", false, false, err
	}
	if mode == "store" {
		// Store-mode envelopes are ALWAYS placed in the store — that is the
		// source of truth for a pending (unacknowledged) message, so an ack can
		// read-destroy it and emit a read receipt, and the purge can expire it,
		// whether or not the recipient happens to be online right now. If the
		// recipient IS online we also push it live; if not, it waits in the queue
		// for the next stream. The queue caps bound server load either way.
		if qerr := e.store.Enqueue(env); qerr != nil {
			return "", false, false, qerr
		}
		delivered = e.hub.Publish(env)
		return env.ID, delivered, !delivered, nil
	}
	// live mode: fire-and-forget. Deliver only if the recipient is online now;
	// if offline, drop it — never queued, never stored, no receipt.
	delivered = e.hub.Publish(env)
	return env.ID, delivered, false, nil
}

// Ack read-destroys an envelope and, if the sender is streaming, emits a read
// receipt to them.
func (e *Engine) Ack(id string) {
	if sender, existed := e.store.Delete(id); existed {
		e.hub.PublishReceipt(sender, id, "read")
	}
}

// AckReturningSender is Ack that also reports the message's sender, so a caller
// can forward the read receipt to a sender who is not on this instance (an
// onion-to-onion peer, ADR-0142). Returns ok=false if the id was unknown.
func (e *Engine) AckReturningSender(id string) (sender string, ok bool) {
	sender, existed := e.store.Delete(id)
	if !existed {
		return "", false
	}
	e.hub.PublishReceipt(sender, id, "read")
	return sender, true
}

// PublishReceipt delivers a receipt (e.g. "read") to a streaming user. Used to
// surface a receipt that arrived from another onion for a message this instance's
// user sent (ADR-0142).
func (e *Engine) PublishReceipt(user, id, status string) {
	e.hub.PublishReceipt(user, id, status)
}

// PubKey resolves a recipient's armored public key and fingerprint.
func (e *Engine) PubKey(email string) (armored, fingerprint string, err error) {
	if e.pubkey == nil {
		return "", "", errors.New("vayutalk: pubkey provider unavailable")
	}
	return e.pubkey(email)
}

// EnvelopePayloadFor exposes the wire projection of an envelope so the SSE
// handler can flush queued envelopes on stream open.
func EnvelopePayloadFor(env *Envelope) EnvelopePayload { return envelopePayload(env) }

// encodeCiphertext base64-encodes opaque ciphertext for the wire.
func encodeCiphertext(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
