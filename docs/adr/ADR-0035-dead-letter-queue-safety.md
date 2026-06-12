# ADR-0035: Dead-Letter Queue Safety Controls

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

The write queue's dead-letter replay was unbounded. A poison job (e.g., a write job with malformed JSON that always fails) could be replayed indefinitely, consuming worker capacity and filling logs.

## Decision

1. Add `replay_count` (INTEGER, default 0) and `dead_reason` (TEXT) columns to `write_jobs`.
2. Replay is limited to `MAX_REPLAY_COUNT` attempts (default 3, configurable via env).
3. Replay is batched — maximum `REPLAY_BATCH_LIMIT` (default 100) jobs per replay call.
4. After `MAX_REPLAY_COUNT` replays, the job is **quarantined**: status set to `quarantined`, `dead_reason` set to `"max_replay_count_exceeded"`, and `metricPoisonJobsQuarantined` incremented.
5. Quarantined jobs are not processed further. They are retained for manual inspection.
6. Backoff is capped at `maxBackoffSeconds = 300` (5 minutes) to prevent `math.Pow(2, retry)` overflow.

## Rationale

Without replay limits, a single malformed article write job (e.g., from a corrupt API client) can starve the queue. The quarantine mechanism preserves the job for debugging while protecting worker capacity.

## Consequences

- Positive: Poison jobs cannot starve the queue.
- Positive: Dead jobs are retained for inspection, not silently discarded.
- Negative: Quarantined jobs require manual intervention to resolve (either fix the data or delete).
