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
| **Authenticity** | **Sigstore signature, pinned to this project's release workflow identity** |
| Executable check | The verified bytes must be an executable image for this platform |
| Apply | Atomic rename, previous binary kept as `.bak` |
| Restart | Operator action only — never automatic |

### Why signatures, not just checksums

A checksum proves the bytes match the *published* checksum file. If the release
channel is compromised, the attacker controls both the binary and its checksum —
they are published together and fetched over the same connection, so a checksum
is a self-certificate.

Authenticity is therefore the release's Sigstore signature. It is verified
against a trust root **compiled into this binary**, so verification needs no
network and cannot silently degrade when a fetch fails.

A Sigstore signature on its own proves only that *somebody* signed — anyone can
run a CI job and obtain a valid certificate. The security is in the identity
policy: the signature must come from

- SAN `https://github.com/johalputt/VayuPress/.github/workflows/tag-release.yml@refs/heads/main`
- issuer `https://token.actions.githubusercontent.com`

The branch is part of that identity deliberately. The same workflow filename on
another branch, or in a fork, produces a perfectly valid signature carrying a
different SAN, and a policy matching only the repository would accept it.

Signing certificates are keyless and live about ten minutes, so every release is
signed by a certificate that has long expired by the time anyone installs it.
Validity is therefore judged as of a signed record of *when* the signature was
made, not the current time. Two independent observers supply that record: the
transparency log entry, whose inclusion in the log is verified before its
timestamp is trusted, and an RFC3161 countersignature from a timestamp authority
anchored in the same embedded trust root.

Requiring such a timestamp is not the same as verifying one. v3.17.29 asked for
the observer-timestamp threshold without asking for the transparency log entry to
be verified — and verifying that entry is what produces the timestamp — so the
threshold was compared against an empty set and every genuine release was
rejected. It is recorded here because the shape of the mistake outlives it: a
policy clause that consumes a value proves nothing unless some other clause
produces it.

### Gates (all must pass before any filesystem change)

1. System mode ∉ {`read-only`, `quarantined`, `maintenance`}
2. Checksum verifies
3. A signature bundle exists for the binary — a release without one is refused,
   never accepted on its checksum
4. That signature verifies, and its certificate identity matches the workflow above
5. The verified bytes are an executable image for this platform
6. Database backed up (optional; the operator's choice, and never silent)

`VAYU_RELEASE_PUBKEY` is an **optional additional** Ed25519 pin for operators who
maintain their own key. It is not required, and it is no longer what provides
authenticity.

The CLI path additionally requires `VAYU_SELFUPDATE_ENABLED=true`.

### The web apply route

`POST /os/api/update/apply` downloads, verifies and replaces the binary;
`/os/api/update/restart` and `/os/api/update/rollback` sit beside it. All three
are admin-role-checked and CSRF-protected, and all three run the verification
above — there is no flag, environment variable or request field that turns it
off.

> An earlier version of this document stated that no endpoint downloaded,
> replaced or restarted the binary. That described ADR-0064's original CLI-only
> design and had not been true since the VayuOS panel gained one-click updates.
> It is recorded here rather than quietly corrected, because a security document
> that under-describes the attack surface is itself a finding.

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
