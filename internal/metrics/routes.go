// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"sort"
	"sync"
	"time"
)

// RouteLatency is HTTP latency per route over the same window as
// HTTPLatencyWindow, so the one figure the Monitoring page shows can say which
// requests make it.
//
// It exists because the figure alone could not be acted on: johal.in read a
// p95 of 1451 ms with nothing to say whether that was the pages visitors read,
// the console, or a handful of large transfers, and finding out meant reading
// the server's log by hand.
//
// Keyed by the router's pattern ("GET /os/d/{id}/website"), never by the path
// as requested: a pattern is one of a fixed set the binary registers, so the map
// stays bounded however many distinct addresses are asked for.
var RouteLatency RouteWindows

// RouteWindows holds one WindowedHistogram per route.
type RouteWindows struct {
	mu sync.Mutex
	m  map[string]*WindowedHistogram
}

// Record adds one request's duration to its route.
func (r *RouteWindows) Record(route string, d time.Duration) {
	r.mu.Lock()
	h := r.m[route]
	if h == nil {
		if r.m == nil {
			r.m = map[string]*WindowedHistogram{}
		}
		h = &WindowedHistogram{}
		r.m[route] = h
	}
	r.mu.Unlock()
	h.Record(d)
}

// RouteStat is one route's share of the window.
type RouteStat struct {
	Route    string `json:"route"`
	Requests int64  `json:"requests"`
	P95      int64  `json:"p95_ms"`
	// Total is the time the route's requests took together, in milliseconds,
	// estimated from its histogram. A route read rarely but slowly and one read
	// constantly and a little slowly both show here, which the p95 alone hides.
	Total int64 `json:"total_ms"`
}

// Slowest returns up to n routes seen in the window, slowest p95 first, and
// routes with equal p95 by how many requests they served.
func (r *RouteWindows) Slowest(n int) []RouteStat {
	r.mu.Lock()
	hs := make(map[string]*WindowedHistogram, len(r.m))
	for k, h := range r.m {
		hs[k] = h
	}
	r.mu.Unlock()
	out := make([]RouteStat, 0, len(hs))
	for route, h := range hs {
		count, total := h.countAndTotal()
		if count == 0 {
			continue
		}
		out = append(out, RouteStat{Route: route, Requests: count, P95: h.Percentile(95), Total: total})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].P95 != out[j].P95 {
			return out[i].P95 > out[j].P95
		}
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].Route < out[j].Route
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// countAndTotal is the window's request count and an estimate of the time they
// took together: each bucket's count at its midpoint.
func (h *WindowedHistogram) countAndTotal() (count, totalMS int64) {
	nowMin := time.Now().Unix() / 60
	h.mu.Lock()
	h.rotate(nowMin)
	var agg [16]int64
	for s := 0; s < windowMinutes; s++ {
		for b := 0; b < 16; b++ {
			agg[b] += h.slots[s][b]
		}
	}
	h.mu.Unlock()
	lower := int64(0)
	for b, c := range agg {
		upper := HistBoundMS[b]
		if upper == 1<<62 {
			upper = lower * 2
		}
		count += c
		totalMS += c * (lower + upper) / 2
		lower = HistBoundMS[b]
	}
	return count, totalMS
}
