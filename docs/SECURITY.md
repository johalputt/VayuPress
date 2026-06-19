# Security

This document covers the security posture of the features added alongside the
admin redesign and self-update system. For the broader threat model see
[THREAT-MODEL.md](THREAT-MODEL.md).

---

## Self-update

The self-update system is designed to **eliminate the remote-code-execution
class** that a naïve "upgrade now" button would introduce. See
[ADR-0064](adr/ADR-0064-sovereign-self-update.md).

### Trust boundaries

| Step | Trust mechanism |
|------|-----------------|
| Check for updates | Read-only; no trust decision, no mutation |
| Download binary | Over TLS to GitHub; bytes are *untrusted* until verified |
| Integrity | SHA-256 vs published checksum (constant-time compare) |
| **Authenticity** | **Ed25519 signature over the digest, verified against an operator-pinned key** |
| Apply | Atomic rename, previous binary kept as `.bak` |
| Restart | Operator action only — never automatic |

### Why signatures, not just checksums

A checksum proves the bytes match the *published* checksum file. If the release
channel is compromised, the attacker controls both the binary and its checksum.
Authenticity requires a signature verified against a key the operator obtained
**out-of-band** (`VAYU_RELEASE_PUBKEY`) and that never travels with the artifact.

### Gates (all must pass before any filesystem change)

1. `VAYU_SELFUPDATE_ENABLED=true` (opt-in; off by default)
2. `VAYU_RELEASE_PUBKEY` present (no key → no apply)
3. System mode ∉ {`read-only`, `quarantined`, `maintenance`}
4. Checksum verifies
5. Ed25519 signature verifies
6. Database backed up

### No web apply

There is exactly one update-related HTTP route: `GET /admin/api/updates/check`
(read-only). There is **no** endpoint that downloads, replaces, or restarts. A
bug on the check route cannot escalate beyond information disclosure of the
latest public release.

---

## Admin UI (`/admin/v2`)

See [ADR-0065](adr/ADR-0065-admin-ui-csp-compliant-stack.md).

- **CSP preserved.** No `unsafe-eval`, no `unsafe-inline`, no third-party
  origins. Tailwind is precompiled; Alpine uses its CSP build; inline scripts
  carry a per-request nonce.
- **No new write surface.** The editor reuses the existing `/api/v1/articles`
  endpoints and the established CSRF cookie/header handshake. `/admin/v2` adds no
  privileged mutation route.
- **Non-breaking.** The legacy `/admin` is untouched, limiting the blast radius
  of any bug in the new UI to the new UI.
- **`noindex`.** All admin pages set `X-Robots-Tag: noindex`.

---

## Plugin feature endpoints

The comment, webmention, and newsletter receivers accept untrusted public input:

| Endpoint | Hardening |
|----------|-----------|
| `POST /api/v1/articles/{slug}/comments` | Stored as `pending`; rendered only after moderation; body sanitized on render |
| `POST /webmention` | Source/target validated; stored `pending`; W3C 202 semantics; no SSRF auto-fetch in the receive path |
| `POST /api/v1/newsletter/subscribe` | Email validated; double-opt-in confirmation token required before active |

Spam classification (`internal/spam`) and the existing rate limiter apply to
these public routes.

---

## Dependency hygiene

All Go modules (core + every tool under `tools/`) are kept current via
`go get -u ./... && go mod tidy`. Note that this remote build environment's
network policy blocks `vuln.go.dev`, so `govulncheck` cannot reach the live
vulnerability database here; dependency currency is maintained by upgrade-and-tidy
plus CI running `govulncheck` where the database is reachable.

---

## Reporting

Report vulnerabilities privately to the maintainers per
[MAINTAINERS.md](MAINTAINERS.md). Do not open public issues for security reports.
