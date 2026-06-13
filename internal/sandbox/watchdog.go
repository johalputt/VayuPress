package sandbox

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// PluginTelemetry accumulates per-plugin runtime metrics.
// All fields are updated atomically so reads are always consistent.
type PluginTelemetry struct {
	InvocationCount atomic.Int64
	SuccessCount    atomic.Int64
	FailureCount    atomic.Int64
	TimeoutCount    atomic.Int64
	TotalDurationMs atomic.Int64
	LastInvokedNsec atomic.Int64 // unix nanoseconds
	WatchdogKills   atomic.Int64
}

// Snapshot returns a point-in-time copy of telemetry (no atomicity guarantee across fields).
type TelemetrySnapshot struct {
	Name          string  `json:"name"`
	Invocations   int64   `json:"invocations"`
	Successes     int64   `json:"successes"`
	Failures      int64   `json:"failures"`
	Timeouts      int64   `json:"timeouts"`
	WatchdogKills int64   `json:"watchdog_kills"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
}

func (t *PluginTelemetry) snapshot(name string) TelemetrySnapshot {
	inv := t.InvocationCount.Load()
	var avg float64
	if inv > 0 {
		avg = float64(t.TotalDurationMs.Load()) / float64(inv)
	}
	return TelemetrySnapshot{
		Name:          name,
		Invocations:   inv,
		Successes:     t.SuccessCount.Load(),
		Failures:      t.FailureCount.Load(),
		Timeouts:      t.TimeoutCount.Load(),
		WatchdogKills: t.WatchdogKills.Load(),
		AvgDurationMs: avg,
	}
}

// watchdog launches a background goroutine that kills the subprocess if
// it runs for longer than hardDeadline after the context is cancelled.
// The caller must ensure ctx is cancelled when the invocation ends.
func watchdog(ctx context.Context, p *SubprocessPlugin, hardDeadline time.Duration) (cancel context.CancelFunc) {
	wctx, wcancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-wctx.Done():
			// Normal path: invocation finished before watchdog fired.
		case <-time.After(hardDeadline):
			// Hard deadline exceeded — kill regardless of ctx.
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.cmd != nil && p.cmd.Process != nil {
				logging.LogJSON(logging.LogFields{
					Level:     "error",
					Component: "sandbox",
					Msg:       "watchdog: hard timeout exceeded — killing plugin " + p.manifest.Name,
				})
				p.telemetry.WatchdogKills.Add(1)
				p.killSubprocess()
			}
		}
	}()
	return wcancel
}
