# ADR-0037: Pprof Explicit Handler + Rate Limit + Audit Log

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

Go's `net/http/pprof` package registers handlers on `http.DefaultServeMux` when imported. Accidentally importing it in production exposes CPU profiles, heap dumps, and goroutine traces — sensitive operational data — to anyone who can reach the server. Even when intentionally enabled, pprof access should be logged and rate-limited.

## Decision

1. The `net/http/pprof` package is **not** imported via blank import. Pprof handlers are registered explicitly on a dedicated sub-router (`/debug/pprof/`).
2. Access to `/debug/pprof/*` requires the `Authorization: Bearer <VAYU_API_KEY>` header — the same admin API key.
3. Pprof endpoint access is rate-limited to 5 requests per minute per IP using a token bucket.
4. Every pprof access (successful or rejected) is written to the `audit_log` table: timestamp, IP, path, status, admin key hash (not the key itself).
5. The `allowPprof` boolean in config controls whether pprof is registered at all. Default: `false` in production, `true` in development.

## Rationale

CPU profiles reveal algorithmic secrets. Heap dumps can contain secrets in memory. Goroutine traces expose internal state. Explicit registration (vs. blank import) prevents accidental enablement. Audit logging ensures any pprof access is traceable.

## Consequences

- Positive: No accidental pprof exposure in production.
- Positive: All pprof access is auditable.
- Positive: Rate limiting prevents pprof from being used as a DoS vector.
- Negative: Developers must explicitly enable pprof in dev environments.
