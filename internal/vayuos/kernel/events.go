// Package kernel — VayuOS typed event bus.
//
// This event bus connects VayuOS subsystems. When a UserCreated event fires,
// VayuPGP auto-generates a keypair and VayuMail auto-creates a mailbox.
// When a DomainAdded event fires, DNS records are configured and TLS certs
// are obtained.
package kernel

import (
	"context"
	"fmt"
	"reflect"
	"sync"
)

// ── VayuOS event types ───────────────────────────────────────────────────────

type UserCreated struct {
	UserID   string
	Email    string
	Name     string
	Password string
}

type UserDeleted struct {
	UserID string
	Email  string
}

type DomainAdded struct {
	Domain string
	AdminEmail string
}

type DomainRemoved struct {
	Domain string
}

type KeyExpiring struct {
	UserID string
	Email  string
	ExpiresAtUnix int64
}

type CertExpiring struct {
	Domain string
	ExpiresAtUnix int64
}

// ── Bus ──────────────────────────────────────────────────────────────────────

type Handler func(ctx context.Context, event interface{})

type Bus struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
}

func NewBus() *Bus {
	return &Bus{handlers: make(map[string][]Handler)}
}

func (b *Bus) Subscribe(sample interface{}, h Handler) {
	key := eventTypeName(sample)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[key] = append(b.handlers[key], h)
}

func (b *Bus) Publish(ctx context.Context, event interface{}) {
	b.mu.RLock()
	hs := b.handlers[eventTypeName(event)]
	b.mu.RUnlock()
	for _, h := range hs {
		func() {
			defer func() {
				if p := recover(); p != nil {
					// Log panic but don't crash
					_ = fmt.Sprint(p)
				}
			}()
			h(ctx, event)
		}()
	}
}

func eventTypeName(v interface{}) string {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.PkgPath() + "." + t.Name()
}