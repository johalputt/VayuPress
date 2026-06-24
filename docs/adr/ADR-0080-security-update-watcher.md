# ADR-0080: Security-Update Watcher — Opt-In, No Telemetry

**Status**: Accepted  
**Date**: 2026-06-24  
**Author**: @johalputt

## Context

VayuPGP/VayuMail rest on a small set of security-critical crypto dependencies
(go-crypto, CIRCL, …). Operators need to know when an upstream security patch
exists — without VayuPress ever phoning home about their site.

## Decision

1. Ship a watcher that compares the **built** versions of tracked dependencies
   (read from the embedded build info) against upstream GitHub release metadata.
2. It is **disabled by default** (`VAYUOS_SECURITY_UPDATES=on` to enable). When
   disabled it performs no network I/O.
3. When enabled it fetches only public release metadata from `api.github.com`
   and **transmits nothing** about the operator or their site — it is not
   telemetry.
4. It never mutates the binary or dependencies; the actual upgrade remains an
   operator action (`go get -u … && go build`). A future milestone adds an
   admin-confirmed update flow.

## Consequences

- Positive: timely security-patch awareness consistent with the zero-telemetry
  constitution; the CIRCL v1.6.2→v1.6.3 advisory in this release is exactly the
  class of issue it surfaces.
- Trade-off: discovery only — applying the patch is a deliberate, reviewed step.
