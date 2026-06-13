# Trust Model — VayuPress

**Status:** Authoritative  
**Last reviewed:** 2026-06-13

---

## Trust Levels

| Level | Entity | What it can do |
|-------|--------|----------------|
| **L0 — Untrusted** | Anonymous HTTP clients, ActivityPub remotes, plugin stdin/stdout | Read public content only; all input validated and escaped |
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
| ActivityPub inbox | L0 (remote actors) | Queued for validation before processing |
| DID authenticator | L0 (challenge/response) | Grants L1 on successful verify |
| AI runtime | L3 internal only | No external network access |
| Signing keys | L4 operator | Signs articles; key not accessible to L0-L2 |

---

## What We Explicitly Do NOT Trust

1. **Plugin stdout** — parsed as untrusted JSON; log lines forwarded after sanitisation
2. **ActivityPub `content` field** — HTML-sanitised before storage or display
3. **Federation actor claims** — HTTP Signature verification required (Ω2)
4. **User-supplied file paths** — validated against allowlist prefix; no traversal
5. **Config values from environment** — schema-validated at startup; invalid = fatal
6. **Plugin binary on disk** — SHA-256 hash checked against `Manifest.ExecutableHash`

---

## Key Management

| Key | Type | Location | Rotation |
|-----|------|----------|----------|
| Article signing key | Ed25519 private | `/etc/vayupress/signing.key` (mode 0600) | Manual; re-sign on rotation |
| DID authentication key | Ed25519 per-DID | Generated per-user; stored in user record | User-initiated |
| TLS certificate | RSA-2048 / ECDSA | Nginx + certbot / Let's Encrypt | Auto-renewed 30d before expiry |

Private keys are never:
- Written to the database
- Logged (even at debug level)
- Included in error messages
- Accessible to plugin subprocesses

---

## Signing Chain

```
Operator generates Ed25519 keypair
  └─ Private key: /etc/vayupress/signing.key (L4 access only)
  └─ Public key:  published in /.well-known/vayupress-signing-key

Author submits article
  └─ ArticlePayload serialised to canonical JSON (sorted keys)
  └─ Ed25519 signature computed by host process (L3)
  └─ SignedArticle stored: {payload, public_key_hex, signature_hex}

Reader fetches article
  └─ signing.Verify() called before serving
  └─ Tampered articles → 500 + incident log
```

---

## Compatibility & Stability Guarantees

See [`docs/compatibility/stability-matrix.md`](../compatibility/stability-matrix.md).
