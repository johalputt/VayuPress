// Package trace propagates request-scoped observability identifiers through
// context.Context so that every log line, event, and queue job can be correlated
// back to the originating HTTP request (ADR-0053).
package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type contextKey int

const (
	correlationIDKey contextKey = iota
	causationIDKey
)

// NewID returns a cryptographically random 16-byte hex string suitable for use
// as a correlation or causation ID.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// WithCorrelationID returns a child context carrying the given correlation ID.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

// CorrelationID extracts the correlation ID from ctx, or returns "" if absent.
func CorrelationID(ctx context.Context) string {
	if v, ok := ctx.Value(correlationIDKey).(string); ok {
		return v
	}
	return ""
}

// WithCausationID returns a child context carrying the given causation ID.
func WithCausationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, causationIDKey, id)
}

// CausationID extracts the causation ID from ctx, or returns "" if absent.
func CausationID(ctx context.Context) string {
	if v, ok := ctx.Value(causationIDKey).(string); ok {
		return v
	}
	return ""
}
