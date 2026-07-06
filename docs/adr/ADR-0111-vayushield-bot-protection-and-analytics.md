# ADR-0111: VayuShield Bot Protection & VayuAnalytics Engagement Analytics

**Status**: Accepted  
**Date**: 2026-07-06  
**Author**: @johalputt  
**Relates to**: [ADR-0097](ADR-0097-vayuanalytics-no-pii.md) (cookieless, no-PII analytics)

## Context

VayuPress operators had no native defence against automated abuse — scrapers,
credential-stuffing tools, vulnerability scanners and headless-browser bots —
and no way to distinguish the fast-growing stream of readers arriving via AI
assistants (ChatGPT, Claude, Perplexity, Copilot, Gemini) from ordinary search
traffic or from bad bots. The usual answers are Cloudflare Bot Management and
Google Analytics: both are third-party services that see every visitor, break
the sovereign "nothing leaves the host" posture, and (for GA) require cookies
and a consent banner.

The existing analytics (ADR-0097) already proved a cookieless, IP-hashing,
no-PII model works. We wanted to extend that philosophy to cover both bot
defence and engagement quality without adding a runtime dependency, CGo, or any
external call, and without weakening the constitutional constraints
(pure Go, Apache-2.0 only, single binary, SQLite only, race-clean).

## Decision

**Build VayuShield (bot protection) and VayuAnalytics Enterprise (engagement
analytics) as native, in-binary subsystems**, computing everything locally.

**Detection is layered and explainable.** A client is fingerprinted from signals
Go can observe without any third party:

- **TLS** — JA3 and JA4 derived from the ClientHello (cipher suites, curves,
  ALPN, signature schemes, versions), plus detection of a 2026 post-quantum key
  share (`X25519MLKEM768`) that real browsers offer and most spoofing bots omit.
- **HTTP/2** — the SETTINGS frame's `INITIAL_WINDOW_SIZE` (Go's client default
  of 65535 versus a real browser's large window) and header/pseudo-header order.
- **User-Agent** — matched against a compiled-in static database (good bots, bad
  bots, AI agents) and a SQLite self-learning adaptive database.

These combine into a deterministic composite `BotScore` (0 = human, 1 = bot) and
a `ClientType`. Suspicious traffic meets an **escalating challenge ladder** —
a silent SHA-256 proof-of-work, then a JavaScript interstitial, then a hard
block, then an optional tarpit — with verified visitors carrying a signed,
PII-free session token. **Good bots and AI agents are always allowed and counted
separately, never blocked** — AI-assisted visitors are real readers on a new
channel.

**Analytics stays GDPR-compliant by architecture.** Sessions are a
daily-rotating salted hash of request attributes (never a stored IP, never a
cookie), so a visitor cannot be re-identified across days or linked to PII. On
top of the existing view metrics, VayuAnalytics records real engagement — time
on page, scroll depth, engagement and bounce — and classifies every visit by
source, including a first-class **AI-assisted discovery** category so operators
can compare AI-referred engagement to organic search. A machine-readable
disclosure is published at `/.well-known/privacy-report.json`.

**Decoupling for testability and layering.** The `internal/vayushield`
and `internal/vayuanalytics` packages carry no governance/geoip imports; the
`cmd` layer injects country lookup, error-budget charging and trusted-client-IP
resolution as callbacks. This keeps the engine unit-testable in isolation and
respects the import-layer rules.

**Governance integration.** A new `bot-attack-intensity` error-budget (WARN)
makes a sustained attack visible to the governance layer, charged with
throttling so ordinary traffic never exhausts it.

Bot protection is **off by default** (`VAYUSHIELD=on` to enable); analytics
ingestion is always available so the dashboard has data regardless.

## Consequences

- Positive: operators get Cloudflare-class bot defence and GA-class (plus
  AI-traffic) engagement analytics with **zero third-party services, zero
  external calls, no cookies and no stored PII** — all inside the existing
  binary and SQLite database.
- **Fail-open by design:** any internal error, missing dependency, or disabled
  flag degrades to *allow*. Bot protection can never take the site down, and the
  challenge session cookie is security-essential (no tracking), so no consent
  banner is required.
- **Documented capture limitation:** JA3/JA4 are computed from the fields Go's
  `crypto/tls` exposes via `ClientHelloInfo`; the raw extension list is not
  surfaced by the standard stack, and full ClientHello capture is only active
  when VayuPress terminates TLS in-process (today it is usually terminated
  upstream by nginx). The capture store therefore ships as dormant, forward
  -looking infrastructure (recorded in `scripts/deadcode-allow.txt`) rather than
  parsing raw handshake bytes, keeping the subsystem pure-Go with `net/http`.
- Trade-off: the adaptive signature database and analytics sessions add rows to
  SQLite; both are bounded by a background retention/purge sweep and the memory
  footprint stays within the project's operating envelope.
- Migrations `055` and `056` add the `vayushield_*` and `vayuanalytics_sessions`
  tables and apply automatically; they are additive and require no operator
  action.
