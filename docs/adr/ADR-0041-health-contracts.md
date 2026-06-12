# ADR-0041: Structured Health Contracts

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

A single `/health` endpoint returning `{"status":"ok"}` is insufficient for production operations. Load balancers need a liveness check (is the process alive?) separate from a readiness check (is it ready to serve traffic?). Operators need per-dependency status. Ethics compliance requires a `/health/ethics` endpoint.

## Decision

Implement the following structured health endpoints, all returning `application/json`:

| Endpoint | Purpose | Fields |
|----------|---------|--------|
| `GET /health` | Overall status | `{"status":"ok\|degraded\|down", "version":"..."}` |
| `GET /health/live` | Liveness (Kubernetes probe) | `{"live":true}` — always 200 if process is running |
| `GET /health/ready` | Readiness (accept traffic?) | `{"ready":true\|false, "reason":"..."}` |
| `GET /health/dependencies` | Per-dependency status | `{"database":{"ok":true}, "meilisearch":{"ok":true\|false}, "isso":{"ok":true\|false}}` |
| `GET /health/migrations` | Applied migrations | `{"applied":["0001","0002",...], "pending":[]}` |
| `GET /health/ethics` | Ethics compliance | `{"compliant":true, "principles":8, "charter_version":"1.0"}` |

Rules:
- `/health/live` never returns 5xx (it's a liveness check — if it returns anything, the process is alive)
- `/health/ready` returns 503 during startup, migration, or if the database is unavailable
- `/health/dependencies` checks each dep with a 500ms timeout; Meilisearch and Isso failures degrade but don't down the app
- All health endpoints are unauthenticated (readable by monitoring systems without API key)
- All health endpoints are excluded from rate limiting

## Rationale

Separating liveness from readiness prevents Kubernetes from killing a healthy-but-starting pod. Per-dependency status enables targeted alerts. `/health/ethics` provides a machine-readable signal that the ethics charter is implemented.

## Consequences

- Positive: Load balancers and monitoring tools can target specific health signals.
- Positive: `/health/ethics` gives compliance auditors a verifiable endpoint.
- Negative: 6 health endpoints to maintain instead of 1. Mitigated by centralizing in `healthHandler()`.
