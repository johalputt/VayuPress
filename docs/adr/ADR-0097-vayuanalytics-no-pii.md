# ADR-0097: VayuAnalytics — Server-Side Rotating-Salt, No-PII Model

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
   discarded. No online GeoIP, no third-party calls. (Country resolution is
   amended below — see *Amendment 2026-07-04*.)
4. **Session replay is removed.** The public ingest endpoint is body-capped and
   per-IP rate-limited; a retention sweeper prunes detailed rows.

## Consequences

- Positive: real product analytics with no consent banner required and nothing
  to leak on a database compromise.
- Trade-off: cross-day unique-visitor counts are approximate by design (the
  salt rotation is the privacy mechanism).

## Amendment 2026-07-04 — offline country resolution

The original decision recorded "No GeoIP." In practice this meant live
analytics showed **countries only when a CDN/reverse proxy injected a geo
header** (`CF-IPCountry` and friends). A sovereign, CDN-less deployment — the
primary VayuPress target — therefore saw every visitor as *Unknown*, which
reads as a regression even though the code was unchanged.

The amendment resolves this **without weakening the privacy posture**:

- Country is now also resolved by an **offline, embedded** IP→country table
  (`internal/geoip`, backed by `github.com/phuslu/iploc`). It is compiled into
  the binary, performs **no network I/O**, and contacts **no third party** — so
  "no phone-home" is intact. The original "No GeoIP" is narrowed to mean *no
  online GeoIP and no external database download*.
- Proxy headers still win when present; the offline table is a fallback used
  only when no header supplies a usable country.
- The visitor IP is used **only** for this in-process lookup and is **still
  never stored** — analytics persists the country code alone, consistent with
  decision (1). Resolution is coarse country-level only (no city/lat-long).
- Operators who prefer to derive **nothing** from the visitor IP set
  `ANALYTICS_GEOIP=off`; the fallback then returns empty and behaviour matches
  the pre-amendment model exactly.

This keeps the no-consent-banner, no-PII, nothing-to-leak-on-compromise
guarantees while making the sovereign default useful out of the box.
