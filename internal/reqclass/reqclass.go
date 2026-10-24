// SPDX-License-Identifier: Apache-2.0

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

type mark struct {
	shielded atomic.Bool
	site     atomic.Pointer[string]
	cache    atomic.Int32 // 0 not decided, cacheHit or cacheMiss
}

const (
	cacheHit  = 1
	cacheMiss = 2
)

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

// SetSite records the site the request was resolved to ("" the primary, else
// a hosted site's id), so the outer latency recorder can count the request as
// that site's. No-op if the context carries no classification.
func SetSite(ctx context.Context, site string) {
	if m, ok := ctx.Value(ctxKey{}).(*mark); ok {
		m.site.Store(&site)
	}
}

// Site is the site SetSite recorded, and whether one was.
func Site(ctx context.Context) (string, bool) {
	if m, ok := ctx.Value(ctxKey{}).(*mark); ok {
		if p := m.site.Load(); p != nil {
			return *p, true
		}
	}
	return "", false
}

// MarkCache records whether the page cache answered the request (hit) or the
// page had to be rendered (a miss), so the outer recorder can count the
// site's pages by it. No-op if the context carries no classification.
func MarkCache(ctx context.Context, hit bool) {
	if m, ok := ctx.Value(ctxKey{}).(*mark); ok {
		if hit {
			m.cache.Store(cacheHit)
		} else {
			m.cache.Store(cacheMiss)
		}
	}
}

// Cache is what MarkCache recorded: whether the page cache answered, and
// whether the page cache decided at all (a page it does not hold is neither).
func Cache(ctx context.Context) (hit, decided bool) {
	if m, ok := ctx.Value(ctxKey{}).(*mark); ok {
		c := m.cache.Load()
		return c == cacheHit, c != 0
	}
	return false, false
}
