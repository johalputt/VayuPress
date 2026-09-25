# Trust Model — VayuPress

**Status:** Authoritative  
**Last reviewed:** 2026-06-13

---

## Trust Levels

| Level | Entity | What it can do |
|-------|--------|----------------|
| **L0 — Untrusted** | Anonymous HTTP clients, plugin stdin/stdout | Read public content only; all input validated and escaped |
| **L1 — Authenticated user** | Authors with valid session token | Create/edit own content; cannot access admin endpoints |
| **L2 — Admin** | Authenticated admin session | Full content management; plugin install/uninstall; config changes |
| **L3 — Host process** | VayuPress Go binary (non-root) | DB reads/writes; subprocess management; metric collection |
| **L4 — Operator** | SSH access to host machine | Systemd management; backup restore; key rotation |

---

## Component Trust Assignments

| Component | Trust level of its callers | Trust level it grants |
|-----------|---------------------------|----------------------|
| Nginx | L0 (internet) | Forwards sanitised requests to L3 |
| Go HTTP handlers | L0-L1-L2 (depending on auth) | Invoke L3 services |
| Plugin subprocess | Treated as L0 (untrusted) | Reads PLUGIN_SCRATCH only |
| SQLite DB | Accessible only to L3 | Authoritative data store |

---

## What We Explicitly Do NOT Trust

1. **Plugin stdout** — parsed as untrusted JSON; log lines forwarded after sanitisation
2. **User-supplied file paths** — validated against allowlist prefix; no traversal
3. **Config values from environment** — schema-validated at startup; invalid = fatal
4. **Plugin binary on disk** — SHA-256 hash checked against `Manifest.ExecutableHash` when the manifest carries one

---

## Key Management

| Key | Type | Location | Rotation |
|-----|------|----------|----------|
| TLS certificate | RSA-2048 / ECDSA | Nginx + certbot / Let's Encrypt | Auto-renewed 30d before expiry |
| DKIM signing key | RSA | `dkim_<selector>.pem` in the mail data directory (mode 0600) | Operator-initiated |
| VayuPGP account keys | OpenPGP | Key files (mode 0600), private key AES-256-GCM encrypted | User-initiated |
| Tor onion service keys | Ed25519 | `tor_onions.private_key` in the database | Per onion; replaced with the onion |

Private keys are never:
- Logged (even at debug level)
- Included in error messages
- Accessible to plugin subprocesses

The onion service keys are the one set held in the database, so a database
backup carries them and is as sensitive as the onion addresses themselves.

---

## Compatibility & Stability Guarantees

See [`docs/compatibility/stability-matrix.md`](../compatibility/stability-matrix.md).
