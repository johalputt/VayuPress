# ADR-0032: Plugin Pool WaitGroup Drain + Context Cancellation

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

The plugin goroutine pool was not draining cleanly on shutdown. Workers could be mid-execution when `close(pluginQueue)` was called, causing panics or lost work. Context cancellation was not propagated to plugin goroutines.

## Decision

1. Add a `sync.WaitGroup` (`workerPluginWg`) that each plugin goroutine increments on start and decrements on exit.
2. Propagate the shutdown context to all plugin goroutines so they exit cleanly on cancellation.
3. On shutdown: `close(pluginQueue)` → `workerPluginWg.Wait()` to ensure all in-flight plugin jobs complete.
4. Each goroutine wraps its execution in `recover()` to isolate panics — a panicking plugin cannot crash the process.
5. `pluginFailures` uses atomic operations. `pluginDisabled` uses `sync.Map`.

## Rationale

Without WaitGroup drain, a shutdown race where a plugin goroutine is mid-write to SQLite corrupts the write job state and leaves articles in `processing` limbo. The stuck-job reaper recovers these on next start, but it's operationally cleaner to drain first.

## Consequences

- Positive: Clean shutdown; no stuck jobs from plugin mid-execution.
- Positive: Panic isolation — a bad plugin does not crash VayuPress.
- Negative: Shutdown may be delayed by up to one plugin job's processing time (~1s typical).
