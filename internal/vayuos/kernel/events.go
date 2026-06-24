// Package kernel is the VayuOS nervous system: a typed in-process event bus,
// an ordered boot orchestrator, and a health monitor. It has no external
// dependencies and never spawns a separate process — VayuOS is the control
// layer living inside the single VayuPress binary.
package kernel

import (
	"context"
	"reflect"
	"sync"
)

// Event is any value published on the bus; concrete event types are matched by
// their Go type.
type Event interface{}

// Handler reacts to a published event.
type Handler func(ctx context.Context, ev Event)

// Bus is a typed publish/subscribe event bus. Handlers are invoked
// synchronously in subscription order so causal ordering is preserved.
type Bus struct {
	mu       sync.RWMutex
	handlers map[reflect.Type][]Handler
}

// NewBus creates an empty event bus.
func NewBus() *Bus {
	return &Bus{handlers: make(map[reflect.Type][]Handler)}
}

// Subscribe registers h for events of the same dynamic type as sample. Pass a
// zero value of the event type as sample, e.g. Subscribe(UserCreated{}, fn).
func (b *Bus) Subscribe(sample Event, h Handler) {
	t := reflect.TypeOf(sample)
	b.mu.Lock()
	b.handlers[t] = append(b.handlers[t], h)
	b.mu.Unlock()
}

// Publish delivers ev to every handler registered for its type.
func (b *Bus) Publish(ctx context.Context, ev Event) {
	t := reflect.TypeOf(ev)
	b.mu.RLock()
	hs := b.handlers[t]
	b.mu.RUnlock()
	for _, h := range hs {
		h(ctx, ev)
	}
}

// ── Event types ──────────────────────────────────────────────────────────────

// UserCreated fires when a VayuPress account is created. VayuOS responds by
// auto-generating a PGP keypair and provisioning a mailbox.
type UserCreated struct {
	UserID string
	Name   string
	Email  string
}

// UserDeleted fires when an account is removed.
type UserDeleted struct {
	UserID string
	Email  string
}

// DomainAdded fires when a mail domain is configured.
type DomainAdded struct {
	Domain string
}

// DomainRemoved fires when a mail domain is removed.
type DomainRemoved struct {
	Domain string
}

// KeyExpiring fires ahead of a PGP key's expiry so it can be rotated.
type KeyExpiring struct {
	UserID string
	Email  string
}
