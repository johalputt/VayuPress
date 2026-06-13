package trace

import (
	"sync/atomic"
	"time"
)

// SamplePolicy controls which spans are retained in the Recorder.
// Errors and slow spans are always retained regardless of the sample rate.
type SamplePolicy struct {
	// Rate is the fraction of non-error, non-slow spans to keep (0.0–1.0).
	// 1.0 = keep all (default), 0.1 = keep 10%.
	Rate float64
	// SlowThreshold: spans with DurationMS >= this are always retained.
	SlowThreshold time.Duration
	counter       atomic.Uint64
}

// DefaultPolicy keeps all spans. Adjust Rate and SlowThreshold to reduce volume.
var DefaultPolicy = &SamplePolicy{Rate: 1.0, SlowThreshold: 500 * time.Millisecond}

// Keep returns true if the given finished span should be stored in the Recorder.
func (p *SamplePolicy) Keep(s *Span) bool {
	// Always retain errors.
	if s.Status == StatusError {
		return true
	}
	// Always retain slow spans.
	if s.DurationMS >= p.SlowThreshold.Milliseconds() {
		return true
	}
	// Rate-limit the rest.
	if p.Rate >= 1.0 {
		return true
	}
	if p.Rate <= 0 {
		return false
	}
	// Deterministic modulo sampling — no random allocation.
	n := p.counter.Add(1)
	threshold := uint64(1.0 / p.Rate)
	return n%threshold == 0
}
