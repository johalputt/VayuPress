# ADR-0108: VayuMail Autoconfig Discovery & Public-Discovery Hardening

**Status**: Accepted  
**Date**: 2026-07-04  
**Author**: @johalputt  
**Relates to**: [ADR-0106](ADR-0106-vayumail-namespace-rotating-qr.md) (VayuMail onboarding), [ADR-0076](ADR-0076-vayupgp-at-rest-keys.md) (VayuPGP keys / WKD)

## Context

VayuMail-Mobile can onboard an account by scanning a QR or pasting a setup code
(ADR-0106). Both carry the server coordinates in the payload. For a returning or
manual setup, the operator otherwise had to type the IMAP/SMTP host and ports by
hand. VayuPress already served a Mozilla/Thunderbird autoconfig XML for generic
mail clients, but nothing exposed those coordinates in a shape the first-party
app could consume cleanly, and the app could not set up an account from an email
address alone.

Separately, the two products independently implement the WKD address hash
(z-base-32 of SHA-1 over the lowercased local-part). The server publishes a key
at `/.well-known/openpgpkey/hu/<hash>`; the app looks it up at the hash it
computes itself. A silent divergence between the two implementations would break
key discovery with no error on either side.

Finally, the unauthenticated `.well-known` discovery endpoints were unhardened:
the WKD handler scans the entire keystore on every request (a DoS amplifier),
sent no cache validators (clients refetched keys endlessly), and the app trusted
whatever a WKD lookup returned without checking the key matched the requested
address.

## Decision

### First-party mail autoconfig

- VayuPress serves `GET /.well-known/vayumail/autoconfig.json` — a small,
  versioned (`vayumail-autoconfig/1`) JSON document carrying the mail server's
  IMAP/POP3/SMTP host, ports, TLS mode, username policy and declared auth
  mechanism. It contains only the public coordinates already shown on the
  Connect tab, never a secret; it 404s when VayuMail is inactive.
- VayuMail-Mobile's `account.DiscoverAutoconfig` fetches it (with an
  `autoconfig.<domain>` fallback) and returns a prefilled account `Config`. The
  manual account screen gains an "Auto-detect from email" button. The document
  shape is pinned **byte-for-byte** by a contract test duplicated in both repos,
  so server output and client parser can never silently drift.

### WKD interop, locked

- An expanded known-answer vector table for the WKD address hash is kept
  identical in both repos' test suites, so any drift between the two independent
  z-base-32/SHA-1 implementations fails CI on whichever side moved.

### Public-discovery hardening

- **Rate limiting.** A dedicated, generous per-IP limiter
  (`PublicDiscoveryRateLimit`, default 240 req / 10 min, `DISCOVERY_RATE_LIMIT`
  override, trusted IPs bypass) guards the WKD and autoconfig routes; it answers
  a bare `429 + Retry-After` rather than the JSON API envelope. Bucket memory is
  bounded by the existing sweeper.
- **WKD caching.** WKD responses now carry a strong `ETag` (over the key bytes)
  plus `Cache-Control: public, max-age=3600` and honour `If-None-Match` with
  `304`. Correctness survives key rotation (ADR-0076): a changed key changes the
  ETag, so a revalidation returns the new key rather than a stale one.
- **Client-side address verification.** `DiscoverWKD` imports a fetched key only
  if it carries a User ID for the exact address requested, so a hostile or
  misconfigured endpoint cannot get a different-address key mis-associated with a
  contact. Redirect-following is disabled and the domain is validated as a clean
  hostname (SSRF, CWE-918).

## Consequences

- A VayuPress mailbox can be configured from just an email address, over
  standards-based discovery, alongside QR and paste-code onboarding — no new Go
  dependency on either side.
- The WKD/autoconfig wire contracts are enforced by shared, byte-exact tests, so
  the two sovereign codebases stay interoperable as they evolve.
- The public discovery surface is DoS-resistant, cacheable, and the client no
  longer trusts a mismatched key — without weakening the strict CSP or the
  self-hosted, no-CDN model.
