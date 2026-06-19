# ADR-0064 — Sovereign Self-Update: Check-Only Service + Signature-Verified CLI Apply

**Status:** Accepted
**Date:** 2026-06-19
**Deciders:** VayuPress Maintainers

---

## Context

Operators asked for a one-click, in-panel "Backup & Upgrade" button that would
download a new VayuPress binary from GitHub Releases and replace the running
process. That ergonomics goal is real, but the naïve implementation is a
remote-code-execution-by-design feature: a web-triggered control path that
fetches an executable from the internet and runs it. For a project whose
constitution is *sovereignty-first, governance-first, security-obsessed*, that
is the single largest attack surface we could add.

Two failure modes make the naïve design unacceptable:

1. **Integrity ≠ authenticity.** A SHA-256 checksum only proves the bytes match
   the *published* checksum file. If the release channel (or the checksum file)
   is compromised, SHA-256 happily verifies attacker-supplied bytes. Authenticity
   requires a signature checked against a key the operator pinned out-of-band.
2. **Web-reachable self-replacement.** Any auth bypass, CSRF gap, or SSRF on a
   "upgrade now" endpoint becomes full host compromise. The blast radius of a
   bug on that route is the whole server.

## Decision

Split the feature into a **read-only check service** and a **gated, CLI-only
apply path**. Implemented in `internal/update`.

### Read-only check (safe, web-exposed)

- `GET /admin/api/updates/check` queries the GitHub Releases API, compares the
  running version with the latest tag, and returns `{current, latest,
  updateAvailable, changelog}`.
- It writes a `checked` row to `update_history` for audit. It performs **no**
  mutation, download, or restart. This is the only update-related route exposed
  over HTTP.

### Signature-verified apply (CLI-only, multiply gated)

`vayupress update apply [--dry-run]` enforces, in order, before anything is
replaced:

1. **Opt-in.** Refuses unless `VAYU_SELFUPDATE_ENABLED=true`. Off by default.
2. **Pinned key required.** Refuses unless `VAYU_RELEASE_PUBKEY` (hex Ed25519
   public key) is set. The operator pins this out-of-band; it is never fetched.
3. **Mode-gated.** Refuses in `read-only`, `quarantined`, or `maintenance` mode.
4. **Checksum + signature.** Downloads the release binary, verifies its SHA-256
   against the published checksum (constant-time compare), then verifies an
   **Ed25519 signature over that digest** against the pinned public key. Either
   check failing aborts with no filesystem change.
5. **Backup-first.** Snapshots the SQLite database (+ WAL/SHM) to a timestamped
   `.tar.gz` before touching the binary.
6. **Atomic, reversible swap.** Writes the verified binary beside the current
   one and `os.Rename`s it into place, keeping `<binary>.bak`. It does **not**
   exec or restart — it prints instructions for the operator to restart via
   `systemd`. Restart remains an operator action, not an automatic one.

`--dry-run` performs every verification step and reports the result without
replacing the binary.

All attempts (`checked`, `started`, `success`, `failed`, `rolled_back`) are
recorded in `update_history` (migration `017-update-history`).

## Consequences

- The web surface gains exactly one read-only endpoint. There is no HTTP path
  that downloads, replaces, or restarts — eliminating the RCE class entirely.
- Authenticity is enforced cryptographically against an operator-pinned key, not
  inferred from a checksum file that shares a trust domain with the artifact.
- Upgrades remain sovereign and auditable: opt-in, mode-respecting, backed up,
  signed, and logged. A compromised GitHub release cannot produce a running
  attacker binary without also holding the operator's offline signing key.
- Trade-off: applying an update is a deliberate shell action, not a button. This
  is intentional — the privilege to replace the running binary should require
  shell access to the host, which already implies full control.
- Future work (not in this ADR): per-release signature manifests published by CI,
  and an optional `systemd`-side helper for operators who want supervised
  restart after a verified swap.
