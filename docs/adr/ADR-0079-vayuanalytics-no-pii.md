# ADR-0079: VayuAnalytics — Server-Side Rotating-Salt, No-PII Model

**Status**: Accepted  
**Date**: 2026-06-24  
**Author**: @johalputt

## Context

v1.8.0 expands analytics (sessions, pages, referrers, events, funnels,
retention, revenue). The original feature branch used a persistent
`localStorage` visitor id plus device fingerprinting and session replay — at
odds with VayuPress's cookieless, no-PII, GDPR-by-default stance.

## Decision

1. Visitor/session identity is derived **server-side** as a one-way SHA-256 hash
   of `(daily-rotating-random-salt + IP + User-Agent + host)`. The salt rotates
   every UTC day and is held only in memory, so a visitor is **unlinkable across
   days** and the raw IP/User-Agent are **never stored**.
2. The tracking script sets **no cookies** and writes **nothing** to
   `localStorage`/`sessionStorage`.
3. Browser/OS/device are reduced to coarse buckets server-side; the UA is
   discarded. No GeoIP, no third-party calls.
4. **Session replay is removed.** The public ingest endpoint is body-capped and
   per-IP rate-limited; a retention sweeper prunes detailed rows.

## Consequences

- Positive: real product analytics with no consent banner required and nothing
  to leak on a database compromise.
- Trade-off: cross-day unique-visitor counts are approximate by design (the
  salt rotation is the privacy mechanism).
