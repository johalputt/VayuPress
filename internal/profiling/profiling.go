// Package profiling exposes a rate-limited pprof handler and snapshot helpers.
// All endpoints require auth.AllowPprof to pass (IP-based rate limit, ADR-0037).
package profiling

import (
	"fmt"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
)

// Handler returns an http.Handler that serves pprof endpoints under /debug/pprof/
// with IP-based rate limiting. Mount at /debug/pprof/.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", guard(pprof.Index))
	mux.HandleFunc("/debug/pprof/cmdline", guard(pprof.Cmdline))
	mux.HandleFunc("/debug/pprof/profile", guard(pprof.Profile))
	mux.HandleFunc("/debug/pprof/symbol", guard(pprof.Symbol))
	mux.HandleFunc("/debug/pprof/trace", guard(pprof.Trace))
	return mux
}

func guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if !auth.AllowPprof(ip) {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		metrics.MetricPprofAccesses++
		logging.LogJSON(logging.LogFields{
			Level:     "info",
			Component: "profiling",
			Msg:       fmt.Sprintf("pprof access: %s %s", ip, r.URL.Path),
		})
		next(w, r)
	}
}

// RuntimeSnapshot returns a point-in-time snapshot of Go runtime memory stats.
type RuntimeSnapshot struct {
	Timestamp      time.Time        `json:"timestamp"`
	Goroutines     int              `json:"goroutines"`
	MemStats       runtime.MemStats `json:"mem_stats"`
	GCPauseNs      uint64           `json:"gc_pause_ns_last"`
	HeapAllocBytes uint64           `json:"heap_alloc_bytes"`
	HeapSysBytes   uint64           `json:"heap_sys_bytes"`
	StackInUse     uint64           `json:"stack_in_use_bytes"`
	NumGC          uint32           `json:"num_gc"`
}

// Snapshot captures current Go runtime memory and goroutine state.
func Snapshot() RuntimeSnapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	snap := RuntimeSnapshot{
		Timestamp:      time.Now().UTC(),
		Goroutines:     runtime.NumGoroutine(),
		MemStats:       ms,
		HeapAllocBytes: ms.HeapAlloc,
		HeapSysBytes:   ms.HeapSys,
		StackInUse:     ms.StackInuse,
		NumGC:          ms.NumGC,
	}
	if ms.NumGC > 0 {
		snap.GCPauseNs = ms.PauseNs[(ms.NumGC+255)%256]
	}
	return snap
}
