// Package events provides a synchronous, in-process typed event bus.
// Domain events replace string-keyed hook payloads (ADR-0050).
package events

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/johalputt/vayupress/internal/logging"
)

// Article mutation events. Each carries the minimum fields needed by subscribers.
type ArticleCreated struct {
	ID   string
	Slug string
	Tags []string
}

type ArticleUpdated struct {
	ID   string
	Slug string
	Tags []string
}

type ArticleDeleted struct {
	ID   string
	Slug string
}

// Handler is a function that handles a single event. The event value is always
// a pointer to one of the typed structs above.
type Handler func(ctx context.Context, event interface{})

// Bus dispatches domain events to registered handlers synchronously.
// Panics inside handlers are recovered and logged so one bad handler cannot
// crash the worker goroutine.
type Bus struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
}

// NewBus returns an initialised event bus.
func NewBus() *Bus {
	return &Bus{handlers: make(map[string][]Handler)}
}

// Subscribe registers h to be called for events of type T. T must be one of
// the exported event structs in this package; pass a zero value to name the type:
//
//	bus.Subscribe(events.ArticleCreated{}, myHandler)
func (b *Bus) Subscribe(sample interface{}, h Handler) {
	key := typeName(sample)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[key] = append(b.handlers[key], h)
}

// Publish dispatches event to all handlers registered for its concrete type.
// Each handler runs synchronously; panics are recovered and logged.
func (b *Bus) Publish(ctx context.Context, event interface{}) {
	b.mu.RLock()
	hs := b.handlers[typeName(event)]
	b.mu.RUnlock()
	for _, h := range hs {
		func() {
			defer func() {
				if p := recover(); p != nil {
					logging.LogError("events", fmt.Sprintf("handler panic for %s", typeName(event)), fmt.Sprint(p))
				}
			}()
			h(ctx, event)
		}()
	}
}

func typeName(v interface{}) string {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.PkgPath() + "." + t.Name()
}
