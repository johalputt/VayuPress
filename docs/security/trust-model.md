# Trust Model — VayuPress

**Status:** Authoritative  
**Last reviewed:** 2026-10-25 (checked against the code)

---

## Trust Levels

| Level | Entity | What it can do |
|-------|--------|----------------|
| **L0 — Untrusted** | Anonymous HTTP clients, plugin stdin/stdout | Read public content only; all input validated and escaped |
| **L1 — Authenticated user** | An `author` (own content) or `editor` (all content, publishing) session | Write content as the role allows; no user, role or system settings |
| **L1c — Client** | An agency `client` session (ADR-0152) | A fixed surface scoped to one domain (`ClientDomainID`): its site and mail, no authoring; not a rung on the ladder above |
| **L2 — Admin** | An `admin` session | Users and roles, system settings, plugins, every site |
| **L3 — Host process** | VayuPress Go binary (non-root) | DB reads/writes; subprocess management; metric collection |
| **L4 — Operator** | SSH access to host machine | Systemd management; backup restore; key rotation |

---

## Component Trust Assignments

| Component | Trust level of its callers | Trust level it grants |
|-----------|---------------------------|----------------------|
| Reverse proxy (nginx, or Caddy, CADDY.md) | L0 (internet) | Terminates TLS; forwards to L3. Client-supplied country headers are blanked by the shipped nginx config |
| Go HTTP handlers | L0-L1-L2 (depending on auth) | Invoke L3 services |
| Plugin subprocess | Treated as L0 (untrusted) | Reads PLUGIN_SCRATCH only |
| SQLite DB | Accessible only to L3 | Authoritative data store |

---

## What We Explicitly Do NOT Trust

1. **Plugin stdout** — parsed as untrusted JSON; log lines forwarded after sanitisation
2. **User-supplied file paths** — validated against allowlist prefix; no traversal
3. **Config values from environment** — a missing required value stops the start (`config.MustEnv`); a malformed entry in a list such as `TRUSTED_PROXIES` is skipped with a warning, not fatal
4. **Plugin binary on disk** — SHA-256 hash checked against `Manifest.ExecutableHash` when the manifest carries one

---

## Key Management

| Key | Type | Location | Rotation |
|-----|------|----------|----------|
| TLS certificate | RSA-2048 / ECDSA | certbot (Let's Encrypt) for nginx, run by the root helper, never the server process | certbot's renewal |
| DKIM signing key | RSA | `dkim_<selector>.pem` in the mail data directory (mode 0600) | Operator-initiated |
| VayuPGP account keys | OpenPGP | Key files (mode 0600), private key AES-256-GCM encrypted | User-initiated |
| Tor onion service keys | Ed25519 | `tor_onions.private_key`, sealed by the install's secrets store (`internal/secrets`, its key in a 0600 file outside the database) | Per onion; replaced with the onion |
| TOTP secrets | Shared secret | `users.totp_secret`, sealed by the same store | User-initiated |
| API keys | Random token | Only `sha256(token)` is stored (`internal/apikeys`) | Revoke and make a new one |
| Release signing key | Ed25519 | Public half compiled into the binary (`internal/update/releasekey.go`); updates are refused unless signed | Shipped with a release |

Private keys are never:
- Logged (even at debug level)
- Included in error messages
- Accessible to plugin subprocesses

Onion keys and TOTP secrets are held in the database sealed, so a database
backup alone does not open them: it needs the key file too. A database restored
without its key file keeps the sealed onion keys as they are rather than
overwriting them (`cmd/vayupress/vayutor_store.go`).

---

## Compatibility & Stability Guarantees

See [`docs/compatibility/stability-matrix.md`](../compatibility/stability-matrix.md).
