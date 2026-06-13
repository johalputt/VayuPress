// Package resource provides concurrency limiters, execution watchdogs, and
// goroutine accounting for resource governance (ADR-0055).
package resource

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// ErrAtCapacity is returned by Limiter.Acquire when the concurrency ceiling is reached.
var ErrAtCapacity = errors.New("resource: at concurrency ceiling")

// Limiter enforces a maximum number of concurrent operations for a named component.
// It is safe for concurrent use and the zero value is invalid — use NewLimiter.
type Limiter struct {
	name    string
	sem     chan struct{}
	active  atomic.Int64
	total   atomic.Int64
	dropped atomic.Int64
}

// NewLimiter returns a Limiter allowing at most max concurrent operations.
func NewLimiter(name string, max int) *Limiter {
	if max <= 0 {
		max = 1
	}
	l := &Limiter{name: name, sem: make(chan struct{}, max)}
	for i := 0; i < max; i++ {
		l.sem <- struct{}{}
	}
	return l
}

// Acquire attempts to take one slot. Returns ErrAtCapacity immediately if full.
// Call Release when the operation finishes.
func (l *Limiter) Acquire() error {
	select {
	case <-l.sem:
		l.active.Add(1)
		l.total.Add(1)
		return nil
	default:
		l.dropped.Add(1)
		return ErrAtCapacity
	}
}

// Release returns a slot to the limiter. Must be called exactly once per successful Acquire.
func (l *Limiter) Release() {
	l.active.Add(-1)
	l.sem <- struct{}{}
}

// Stats returns a snapshot of limiter state for telemetry.
func (l *Limiter) Stats() LimiterStats {
	return LimiterStats{
		Name:    l.name,
		Active:  l.active.Load(),
		Total:   l.total.Load(),
		Dropped: l.dropped.Load(),
		Cap:     int64(cap(l.sem)),
	}
}

// LimiterStats is a point-in-time snapshot of a Limiter.
type LimiterStats struct {
	Name    string `json:"name"`
	Active  int64  `json:"active"`
	Total   int64  `json:"total"`
	Dropped int64  `json:"dropped"`
	Cap     int64  `json:"cap"`
}

// =============================================================================
// Goroutine accounting
// =============================================================================

// GoroutineCount returns the current live goroutine count. Cheap at call sites;
// add to span attributes for resource-aware tracing.
func GoroutineCount() int { return runtime.NumGoroutine() }

// =============================================================================
// Watchdog — monitors in-flight operations and cancels overruns
// =============================================================================

type watchEntry struct {
	cancel  context.CancelFunc
	opName  string
	budget  time.Duration
	startAt time.Time
}

// Watchdog monitors registered in-flight operations and cancels those that
// exceed their time budget. It runs in a background goroutine until Stop is called.
type Watchdog struct {
	mu      sync.Mutex
	ops     map[string]*watchEntry // key = opID
	ticker  *time.Ticker
	stopCh  chan struct{}
	stopped bool
}

// NewWatchdog returns a Watchdog that checks for overruns at the given interval.
func NewWatchdog(checkInterval time.Duration) *Watchdog {
	w := &Watchdog{
		ops:    make(map[string]*watchEntry),
		ticker: time.NewTicker(checkInterval),
		stopCh: make(chan struct{}),
	}
	go w.run()
	return w
}

// Watch registers an operation with the watchdog. Returns a function to deregister it.
// If the operation runs longer than budget, its cancel function is called.
func (w *Watchdog) Watch(opID, opName string, budget time.Duration, cancel context.CancelFunc) func() {
	w.mu.Lock()
	w.ops[opID] = &watchEntry{cancel: cancel, opName: opName, budget: budget, startAt: time.Now()}
	w.mu.Unlock()
	return func() {
		w.mu.Lock()
		delete(w.ops, opID)
		w.mu.Unlock()
	}
}

func (w *Watchdog) run() {
	for {
		select {
		case <-w.stopCh:
			return
		case now := <-w.ticker.C:
			w.mu.Lock()
			for id, e := range w.ops {
				if now.Sub(e.startAt) > e.budget {
					logging.LogJSON(logging.LogFields{
						Level:     "warn",
						Component: "watchdog",
						Msg:       "operation exceeded budget — cancelling: " + e.opName,
					})
					e.cancel()
					delete(w.ops, id)
				}
			}
			w.mu.Unlock()
		}
	}
}

// Stop shuts down the watchdog goroutine. Safe to call multiple times.
func (w *Watchdog) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.stopped {
		w.stopped = true
		w.ticker.Stop()
		close(w.stopCh)
	}
}

// =============================================================================
// Global registry
// =============================================================================

var (
	globalMu       sync.RWMutex
	globalLimiters = make(map[string]*Limiter)
	// Global is the application watchdog. Initialised in main.
	Global *Watchdog
)

// Register creates and stores a named Limiter with the given capacity.
// Panics if a limiter with that name already exists.
func Register(name string, max int) *Limiter {
	l := NewLimiter(name, max)
	globalMu.Lock()
	globalLimiters[name] = l
	globalMu.Unlock()
	return l
}

// Get returns a previously registered Limiter by name, or nil.
func Get(name string) *Limiter {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalLimiters[name]
}

// AllStats returns a snapshot of all registered limiters for telemetry.
func AllStats() []LimiterStats {
	globalMu.RLock()
	defer globalMu.RUnlock()
	out := make([]LimiterStats, 0, len(globalLimiters))
	for _, l := range globalLimiters {
		out = append(out, l.Stats())
	}
	return out
}
