# ADR-0118: VayuShield Auto-Block, Detection-Driven Learning & Analytics Exclusion

**Status**: Accepted  
**Date**: 2026-07-08  
**Author**: @johalputt  
**Relates to**: [ADR-0111](ADR-0111-vayushield-bot-protection-and-analytics.md) (VayuShield + VayuAnalytics), [ADR-0112](ADR-0112-vayushield-resilience-tiers.md) (resilience tiers), [ADR-0117](ADR-0117-vayuos-read-path-resilience.md) (read-path resilience)

## Context

VayuShield classified and blocked bad bots on every request, but three gaps
weakened it in practice:

1. **No stickiness.** A blocked bad bot was re-classified from scratch on every
   subsequent request. The full classifier ran each time instead of a cheap O(1)
   rejection, and the operator's expectation — "once you catch a bad bot, keep
   blocking it" — was not met.
2. **The signature database barely grew.** Learning only recorded fingerprints
   that were both `Unknown`-classified *and* scored > 0.75. Confirmed bad bots
   (already classified via the static list or a high score) were never folded
   back into `vayushield_signatures`, so the knowledge base could sit unchanged
   for days.
3. **Bad-bot traffic could still reach analytics.** Blocked bots never render a
   page (so never fire the engagement beacon), but a bad bot that executed page
   JS (a headless scraper) could still fire a beacon and be counted.

All fixes had to remain **GDPR-clean**: no plaintext IPs at rest, no PII beyond
what the existing hashed-IP / coarse-UA-family model already permits.

## Decision

- **Auto-block ("detect once, block thereafter").** On an `ActionBlock` /
  `ActionTarpit` verdict, when the operator's auto-block toggle is on, the source
  IP is jailed in the existing O(1) in-memory TTL blocklist. The next request
  from that IP is then dropped by the cheap blocklist gate at the top of the
  middleware, before classification runs. The jailed IP is **in-memory only**,
  on a short auto-expiring TTL — a security legitimate-interest measure, never
  persisted to disk — so no new PII-at-rest is introduced.
- **Detection-driven learning.** The same block path folds the client's
  fingerprint into `vayushield_signatures` as an **auto-learned `bad_bot`
  candidate** (operator-reviewable, modest confidence), off the request path via
  the batched async writer. The signature DB now grows from real detections, not
  just the static seed. Only derived sub-hashes (JA3/JA4/HTTP2/header-order) and
  a coarse UA-family token are stored — never an IP or the full User-Agent.
- **Analytics exclusion.** The engagement beacon records nothing when a request
  classifies as a bad bot, keeping bad-bot traffic out of the engagement
  analytics. The block itself is recorded to `vayushield_blocked` (with a salted,
  daily-rotating **hashed** IP), which now also surfaces as an operator-visible
  "Recent blocks" list on the Bot Shield panel.

## Consequences

- Positive: repeat bad-bot requests are rejected in O(1) instead of re-classified;
  the bot knowledge base grows continuously from live detections; the operator
  can see exactly what was blocked; and analytics reflect real (human) engagement
  without bad-bot noise.
- Safety / false positives: auto-learned `bad_bot` signatures are candidates
  (not operator-verified) at modest confidence and are reviewable/dismissable in
  the queue, limiting the blast radius of a coarse fingerprint (e.g. behind a
  reverse proxy where TLS sub-hashes are unavailable). The IP jail is gated by
  the operator's auto-block toggle and TTL-bounded, so a shared-NAT false
  positive self-heals quickly.
- GDPR: unchanged posture — hashed/rotating IPs at rest, in-memory-only jail, no
  full UA or plaintext IP stored. Blocking and learning add no new personal data.
- Trending/pinned widget: investigated as part of this cycle and found correct in
  code (present in the article template, cache-schema-invalidated, cache purged
  on toggle); a widget missing under an existing post is an upstream (browser/CDN)
  cache artefact, resolved operationally — no code change.
