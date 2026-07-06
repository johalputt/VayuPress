# ADR-0112: VayuShield Resilience Tiers & Live Operator Controls

**Status**: Accepted  
**Date**: 2026-07-06  
**Author**: @johalputt  
**Relates to**: [ADR-0111](ADR-0111-vayushield-bot-protection-and-analytics.md) (VayuShield bot protection & analytics)

## Context

VayuShield (ADR-0111) classifies clients and challenges bots, but that is *bot
management*, not *availability protection*. Operators asked: can VayuShield stop
DDoS and similar floods? Honestly, no single in-process feature can — a
volumetric flood saturates the network long before the Go process sees it. But a
great deal of the abuse a real publisher faces (L7 request floods, aggressive
scrapers, credential-stuffing bursts, slow-loris) *can* be absorbed cheaply if
the defenses are layered correctly and, critically, do not slow legitimate
traffic. Operators also wanted to turn each defense on and off from VayuOS
without a restart, and needed the engagement analytics to actually populate.

## Decision

**Adopt a three-tier resilience model with an explicit, honest capability
boundary, and expose the in-binary tier as live VayuOS toggles.**

**Tier 1 — in-binary (application layer), fully sovereign, runtime-toggleable.**
A new `internal/vayushield/resilience` package provides O(1)-per-request
primitives — a sharded token-bucket rate limiter, an in-flight concurrency gate
(load shedding), a TTL blocklist, and a lock-free RPS controller for adaptive
under-attack mode. The middleware is ordered **cheapest-first**
(blocklist → load-shed → rate-limit → classify) so that under load the vast
majority of abusive requests are rejected with an O(1) check *before* any
expensive work (TLS fingerprinting, template rendering, SQLite). Design rules
that keep it from ever harming real traffic:

- Everything defaults **off** and is opt-in.
- **Verified visitors** (an unspoofable signed session token) are exempt from
  rate-limiting and load-shedding — a real reader is never throttled.
- Good bots and AI agents remain always-allowed and counted separately.
- Rate limits carry a generous burst; load shedding trips only at genuine
  saturation; auto-jail requires *sustained* breaches; under-attack mode only
  tightens screening of unknown clients and auto-relaxes.

The runtime-tunable state is published via an atomic pointer so the hot path
reads it lock-free, and the VayuOS panel writes settings + calls `ApplySettings`
to swap it in **with no restart**.

**Tier 2 — kernel/OS, sovereign scripts.** `deploy/vayushield-firewall.sh`
(nftables per-IP connection/rate limits + SYN-flood cookies + conntrack tuning).
This drops connection/packet floods below the app.

**Tier 3 — edge, sovereign config.** `deploy/nginx-vayushield.conf` (per-IP
request/connection shaping and slow-loris timeouts at the reverse proxy).

Tiers 2 and 3 need root or the reverse proxy, so they are **configured on the
server, not toggled from the panel** — the panel documents them with copy-paste
commands and a status note rather than pretending the binary can flip kernel
firewall rules.

## Consequences

- Positive: a small/medium publisher gets genuine, layered resilience against the
  abuse they actually face — entirely sovereignly, no third-party service — with
  every in-binary defense toggleable live from VayuOS.
- **Honest boundary:** a true volumetric flood that saturates the uplink can only
  be absorbed by anycast/scrubbing capacity no single host provides. VayuShield
  states this plainly in the panel and docs rather than overselling. Pure
  sovereignty and nation-state-scale volumetric resistance are in tension;
  Tiers 1–2 are the sovereign answer for realistic threats.
- **Performance:** when everything is off the middleware is a near-zero-cost
  pass-through; when on, the added work is O(1) atomics/map lookups per request,
  and expensive fingerprinting runs only for requests that clear the cheap gates,
  so the 4–32 ms P95 envelope is preserved for legitimate traffic.
- The engagement beacon now ships on public pages (piggybacked on the existing
  analytics script, cookieless, toggleable), so VayuAnalytics reports populate.
- New settings keys (`shield.*`, `analytics.beacon`) are additive; no migration
  is required.
