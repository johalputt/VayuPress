package profiling

import (
	"encoding/json"
	"fmt"
	"runtime"
	"time"
)

// HealthFingerprint is a structured runtime snapshot attached to incident reports.
// It captures the system state at the moment of an anomaly for post-mortem analysis.
type HealthFingerprint struct {
	CapturedAt    time.Time `json:"captured_at"`
	Trigger       string    `json:"trigger"` // e.g. "manual", "oom_warn", "plugin_quarantine"
	Goroutines    int       `json:"goroutines"`
	HeapAllocMB   float64   `json:"heap_alloc_mb"`
	HeapSysMB     float64   `json:"heap_sys_mb"`
	StackInUseMB  float64   `json:"stack_in_use_mb"`
	GCCount       uint32    `json:"gc_count"`
	LastGCPauseMs float64   `json:"last_gc_pause_ms"`
	MutexBlocked  bool      `json:"mutex_profiling_active"`
	BlockBlocked  bool      `json:"block_profiling_active"`
}

// CaptureFingerprint records the current runtime state with a named trigger.
// Safe to call from goroutines and signal handlers (only reads atomic counters).
func CaptureFingerprint(trigger string) HealthFingerprint {
	snap := Snapshot()
	return HealthFingerprint{
		CapturedAt:    snap.Timestamp,
		Trigger:       trigger,
		Goroutines:    snap.Goroutines,
		HeapAllocMB:   float64(snap.HeapAllocBytes) / (1 << 20),
		HeapSysMB:     float64(snap.HeapSysBytes) / (1 << 20),
		StackInUseMB:  float64(snap.StackInUse) / (1 << 20),
		GCCount:       snap.NumGC,
		LastGCPauseMs: float64(snap.GCPauseNs) / 1e6,
		MutexBlocked:  runtime.SetMutexProfileFraction(-1) > 0,
		BlockBlocked:  false, // block profiling has no query API; set via EnableBlockProfiling
	}
}

// JSON returns the fingerprint as an indented JSON string for log embedding.
func (h HealthFingerprint) JSON() string {
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error":"marshal failed: %v"}`, err)
	}
	return string(b)
}

// EnableMutexProfiling enables mutex contention profiling at the given fraction
// (1 = capture every event; 0 = disable). Returns the previous fraction.
func EnableMutexProfiling(fraction int) int {
	return runtime.SetMutexProfileFraction(fraction)
}

// EnableBlockProfiling enables goroutine block profiling at the given rate
// (rate in nanoseconds; 1 = all; 0 = disable).
func EnableBlockProfiling(rate int) {
	runtime.SetBlockProfileRate(rate)
}
