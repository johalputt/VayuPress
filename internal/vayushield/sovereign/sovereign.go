// SPDX-License-Identifier: Apache-2.0

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

// Admit reserves one slot for a request. Priority (admin/verified) requests are
// always admitted and never consume the public budget. A public request is
// admitted only while in-flight public requests are under the cap; otherwise it
// is shed. The returned release MUST be called (defer it) — it is a no-op for
// priority requests and for shed requests, so calling it is always safe.
func (g *Gate) Admit(priority bool) (release func(), ok bool) {
	return g.AdmitCost(priority, 1)
}

// AdmitCost is Admit for a request whose route the operator has weighted.
//
// The lane counted every public request as one slot, which meant a 2 ms cached
// article and a 400 ms search reserved the same budget. That is not a rounding
// error: it is the difference between needing thousands of requests per second
// to saturate the lane and needing a few dozen, and an attacker picks the
// expensive route for free. Weighting makes the budget account for WORK rather
// than for arrivals, so filling the lane costs an attacker roughly what serving
// it costs the server.
//
// The reservation is released in exactly the size it was taken, captured at
// admission. Reading the weight again at release time would let a policy edit
// mid-request leak or double-free slots, and a leaked slot never comes back —
// the lane would shrink silently every time an operator saved the page.
func (g *Gate) AdmitCost(priority bool, cost int) (release func(), ok bool) {
	if priority {
		return noop, true
	}
	c := g.clampCost(cost)
	if g.inflight.Add(c) > g.Cap() {
		g.inflight.Add(-c)
		g.shed.Add(1)
		return noop, false
	}
	g.admitted.Add(1)
	if c == 1 {
		return g.release, true // the common path keeps its allocation-free release
	}
	return func() { g.inflight.Add(-c) }, true
}

// minConcurrentPerRoute is how many requests the most expensive route must
// still be able to run at once, and it is what bounds a route's weight.
const minConcurrentPerRoute = 4

// clampCost bounds a declared weight to a fraction of the live cap.
//
// Without this, a weight larger than the cap means the FIRST request on that
// route already exceeds the budget, so every request on it sheds forever: a
// total outage on one route, produced by a number an operator typed, on a
// machine whose cap they cannot see. An admission controller that can be
// configured into refusing a route outright has become the outage it exists to
// prevent. The floor of 1 covers the same hazard from the other end — a
// small-cap machine where cap/4 rounds to zero, which would make every
// reservation free and switch the lane off.
func (g *Gate) clampCost(cost int) int64 {
	if cost < 1 {
		return 1
	}
	maxCost := g.Cap() / minConcurrentPerRoute
	if maxCost < 1 {
		maxCost = 1
	}
	if int64(cost) > maxCost {
		return maxCost
	}
	return int64(cost)
}

func (g *Gate) release() { g.inflight.Add(-1) }

func noop() {}

// Inflight returns the current public in-flight count.
func (g *Gate) Inflight() int64 { return g.inflight.Load() }

// Shed returns the cumulative number of shed public requests.
func (g *Gate) Shed() int64 { return g.shed.Load() }

// Admitted returns the cumulative number of admitted public requests.
func (g *Gate) Admitted() int64 { return g.admitted.Load() }
