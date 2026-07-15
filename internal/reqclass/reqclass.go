// Package reqclass carries a small, mutable per-request classification down the
// middleware chain so an INNER middleware can tell an OUTER one something about
// how the request was handled.
//
// The concrete need: the latency recorder (structuredLoggerMiddleware) is the
// OUTERMOST timing wrapper, but VayuShield — which runs INSIDE it — may
// deliberately delay a confirmed-bot request (a 5s tarpit) or serve a challenge.
// That delay is bot defence, not page latency, and must not be counted in the
// HTTP p95. A plain context value can't flow back UP the chain, so the outer
// middleware seeds a pointer that the inner one mutates in place.
package reqclass

import (
	"context"
	"sync/atomic"
)

type mark struct{ shielded atomic.Bool }

type ctxKey struct{}

// NewContext attaches a fresh, empty classification to ctx. Call this once, in
// the outermost middleware, before serving the rest of the chain.
func NewContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, &mark{})
}

// MarkShielded records that VayuShield enforced against this request (a
// challenge, a tarpit, or a block) — it is bot handling, not a representative
// page load, so downstream latency accounting should skip it. No-op if the
// context carries no classification.
func MarkShielded(ctx context.Context) {
	if m, ok := ctx.Value(ctxKey{}).(*mark); ok {
		m.shielded.Store(true)
	}
}

// Shielded reports whether the request was shield-enforced.
func Shielded(ctx context.Context) bool {
	if m, ok := ctx.Value(ctxKey{}).(*mark); ok {
		return m.shielded.Load()
	}
	return false
}
