# VayuAPI — API Keys, Permissions & Rate Limits

**Status:** Authoritative  
**Last reviewed:** 2026-07-16

---

## Overview

Every programmatic caller of the VayuPress API — a script, a CI job, an
integration, an AI agent, a VCB extension — authenticates with an **API key**
that carries a fine-grained grant set. A key can do exactly what it was
granted, nothing more. This page is the reference the API's own error
responses link to (the `docs` field of an error body points here).

Related: [/docs/compatibility/vcb](/docs/compatibility/vcb) (extension
contracts) · [/docs/API-REFERENCE](/docs/API-REFERENCE) (endpoints) ·
[/docs/adr/ADR-0134-vayuapi-fine-grained-keys](/docs/adr/ADR-0134-vayuapi-fine-grained-keys)
(design record).

---

## Base URL

Call the API at **`https://<your-domain>/api/v1/…`**.

Behind a proxy that shows a bot "challenge" page (e.g. Cloudflare Bot Fight Mode /
managed challenge), a script, CI job or AI agent **cannot** solve it, so those
calls fail. For that case VayuPress can serve the API on a dedicated, proxy-off
host: point `api.<your-domain>` straight at your server (proxy **off / DNS only**),
set `VAYUOS_API_HOST=api.<your-domain>`, and re-run the installer. It provisions a
**hardened** host that exposes **only** `/api` and `/health` (the admin console is
not reachable there), so clients use **`https://api.<your-domain>/api/v1/…`**. The
recommended base URL is shown on **VayuOS → API Keys**. VayuShield still guards the
origin and every call is key-authenticated + rate-limited, so the direct host has a
minimal, safe surface. See [`INSTALLATION.md`](../INSTALLATION.md) → *Direct API host*.

## Authenticating

Send the key with either header on any protected route:

```http
X-API-Key: vp_xxxxxxxxxxxxxxxx
```

```http
Authorization: Bearer vp_xxxxxxxxxxxxxxxx
```

Keys are shown **once**, at creation or rotation. Only a SHA-256 hash is
stored; a lost key cannot be recovered, only rotated.

---

## The capability model

A capability is written `section:action`. A key holds a set of capabilities;
every protected route requires one, and the check is **deny by default** — a
route's capability missing from the key means `403`, and a scoped key hitting
a route outside the mapped table is refused outright.

Twelve sections × six actions — any pair is a grantable capability:

| Vocabulary | Values |
|------------|--------|
| **Sections** | `posts`, `themes`, `design`, `plugins`, `domains`, `analytics`, `media`, `comments`, `members`, `mail`, `settings`, `backup` |
| **Actions** | `read`, `write`, `delete`, `apply`, `install`, `export` |

Examples: `posts:write` (create/edit articles), `themes:apply` (switch the
active theme), `analytics:read`, `backup:export`. Each route requires the
capability matching its intent; a `403` names the exact `section:action` you
are missing, so the error itself tells you what to grant. Granting a pair no
route uses yet is harmless — it simply matches nothing until such a route
exists.

Wildcards exist for operators only: `section:*` grants a section's every
action; `*:*` is a full-access (superuser) key. **Extension manifests may not
declare wildcards** — the VCB validator refuses them.

Exactly two credentials are implicit superusers: the static bootstrap
`API_KEY` environment credential and the internal system key. Every key minted
in the console is grant-limited.

---

## Key lifecycle

Manage keys in **VayuOS → API Keys** (owner-scoped; the full key is shown once
in a copy-once banner):

| Operation | Effect |
|-----------|--------|
| **Create** | Pick a label, tick capabilities in the 12 × 6 grid (or Full access), optionally set an expiry and a per-minute budget. |
| **Rotate** | New secret, same grants; the old value stops working immediately. |
| **Deactivate / Activate** | Reversible disable — the key refuses to authenticate until re-activated. |
| **Revoke** | Permanent disable; the audit row is kept. |
| **Delete** | Removes the key. Keys expired more than 30 days are swept automatically. |
| **Expiry** | Enforced exactly — an expired key is refused on the next request, never lagged by caching. |

For VCB extensions, the manifest's `api_permissions` list is the provisioning
contract: mint the key with exactly those boxes ticked
(see [/docs/compatibility/vcb](/docs/compatibility/vcb)).

---

## Rate limits

Each external key has its own fixed-window budget, keyed by the key's identity
(not the caller's IP — rotating addresses does not escape it):

- Default: **600 requests/minute** (operator-tunable via
  `VAYU_API_RATE_PER_MIN`, or per key at mint time).
- Over budget → `429` with `Retry-After: 60` and this page in the error body.
- The internal system key is exempt; a coarse per-IP limiter still applies in
  front of everything.

Handle `429` by honouring `Retry-After` — do not retry in a tight loop.

---

## Errors

Error bodies are JSON:

```json
{
  "code": "forbidden",
  "message": "this API key does not hold the required capability: posts:write",
  "request_id": "…",
  "docs": "/docs/compatibility/vayuapi"
}
```

| Status | Code | Meaning |
|--------|------|---------|
| `401` | `unauthorized` | Missing or invalid key. |
| `403` | `forbidden` | Valid key, missing capability — the message names the `section:action` you need. |
| `429` | `rate_limited` | Per-key budget exhausted; honour `Retry-After`. |

---

## Audit

Every key-authenticated call is appended to the tamper-evident audit log as
`api.call | key:<id> | METHOD /path | status`, giving the operator a per-key
usage trail that never leaves the box. Design your integrations assuming their
traffic is attributable.
