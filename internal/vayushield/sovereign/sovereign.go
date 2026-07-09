// Package sovereign is VayuShield's Admin Sovereignty Lane (Aegis L0): a
// lock-free admission controller that guarantees the admin control plane and
// verified readers always have CPU/goroutine headroom during a public-traffic
// flood.
//
// The failure it fixes: under a volumetric bot/DDoS hit, unbounded concurrent
// public requests exhaust the scheduler, goroutines and CPU, so even the
// bot-exempt admin console (Save, refresh, VayuOS) times out. Isolating the DB
// read pool (ARDB) is not enough — the process still needs CPU to run the
// handler. This gate caps how many PUBLIC requests may be in flight at once and
// sheds the overflow cheaply BEFORE any expensive work (classification, render,
// SQLite), leaving guaranteed headroom for admin/verified traffic.
//
// The whole hot path is a couple of atomic ops — fixed cost, zero allocation,
// and bounded memory regardless of how many attackers there are.
package sovereign

import (
	"runtime"
	"sync/atomic"
)

// Gate is the admission controller. The zero value is not usable; call New.
type Gate struct {
	max      int64        // CPU-derived default ceiling
	inflight atomic.Int64 // current public requests in flight
	limit    atomic.Int64 // live-tunable cap; 0 = use max
	shed     atomic.Int64 // cumulative shed count (telemetry)
	admitted atomic.Int64 // cumulative public admits (telemetry)
}

// New returns a gate whose default public ceiling is derived from the CPU
// count — generous for real traffic, but never unlimited, so a flood can't
// spawn unbounded concurrent heavy work and starve the admin plane.
func New() *Gate { return &Gate{max: defaultMax()} }

// defaultMax scales the public-concurrency ceiling with the CPU count: 16
// concurrent public requests per core, floored at 32. Public requests are the
// EXPENSIVE kind (classification + render + SQLite; assets and the admin plane
// ride the priority lane), so the cap must be low enough that a flood filling
// it still leaves real CPU headroom — 32 in-flight renders at ~20 ms each is
// ~1,600 req/s of legitimate capacity on a 1-vCPU VPS, while 128+ concurrent
// renders (the previous default) starved the scheduler badly enough that even
// priority requests crawled.
func defaultMax() int64 {
	n := int64(runtime.GOMAXPROCS(0))
	if n < 1 {
		n = 1
	}
	if m := n * 16; m > 32 {
		return m
	}
	return 32
}

// SetLimit live-tunes the public cap. n <= 0 restores the CPU-derived default.
func (g *Gate) SetLimit(n int) {
	if n <= 0 {
		g.limit.Store(0)
	} else {
		g.limit.Store(int64(n))
	}
}

// Cap returns the effective public concurrency ceiling.
func (g *Gate) Cap() int64 {
	if l := g.limit.Load(); l > 0 {
		return l
	}
	return g.max
}

// Admit reserves a slot for a request. Priority (admin/verified) requests are
// always admitted and never consume the public budget. A public request is
// admitted only while in-flight public requests are under the cap; otherwise it
// is shed. The returned release MUST be called (defer it) — it is a no-op for
// priority requests and for shed requests, so calling it is always safe.
func (g *Gate) Admit(priority bool) (release func(), ok bool) {
	if priority {
		return noop, true
	}
	if g.inflight.Add(1) > g.Cap() {
		g.inflight.Add(-1)
		g.shed.Add(1)
		return noop, false
	}
	g.admitted.Add(1)
	return g.release, true
}

func (g *Gate) release() { g.inflight.Add(-1) }

func noop() {}

// Inflight returns the current public in-flight count.
func (g *Gate) Inflight() int64 { return g.inflight.Load() }

// Shed returns the cumulative number of shed public requests.
func (g *Gate) Shed() int64 { return g.shed.Load() }

// Admitted returns the cumulative number of admitted public requests.
func (g *Gate) Admitted() int64 { return g.admitted.Load() }
