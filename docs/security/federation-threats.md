# Federation Threat Model — VayuPress ActivityPub

**Status:** Authoritative  
**Last reviewed:** 2026-06-13

---

## Threat Categories

### T1 — Forged Activity Delivery

**Description:** A remote actor crafts an Activity with a spoofed `actor` field, claiming to be an actor they do not control.

**Current status:** HTTP Signature verification not yet implemented (scheduled Ω2).

**Interim mitigation:**
- Inbox payloads are queued and not immediately acted upon.
- Actor `id` is resolved against the remote server before processing.

**Planned mitigation (Ω2):**
- Verify `Signature` header against actor's public key fetched from `actor.publicKey.publicKeyPem`.
- Reject any inbox delivery where signature is absent or invalid.
- Cache actor public keys with short TTL (15 minutes); re-fetch on verification failure.

---

### T2 — SSRF via Object URLs

**Description:** A federated Activity contains a `url` or `object` field pointing to an internal service (e.g., `http://localhost:8080/admin`).

**Mitigation:**
- URL allowlist validator rejects RFC-1918 addresses, loopback, link-local.
- VayuPress never fetches remote URLs synchronously during request handling.
- All remote fetches occur in background goroutines with timeouts.

---

### T3 — Inbox Flood (ActivityPub DDoS)

**Description:** A malicious or compromised remote server sends high volumes of `Create`, `Delete`, or `Follow` activities.

**Current status:** Basic queue depth cap exists. Per-actor rate limits not yet implemented.

**Planned mitigation (Ω2):**
- Per-actor sliding window rate limit (100 activities/minute).
- Automatic block after sustained flood (10x rate for 5 minutes).
- Admin endpoint to manually block remote actors.

---

### T4 — Content Injection via `content` Field

**Description:** Remote actor sends HTML/JavaScript in the `content` field of a `Note` or `Article`.

**Mitigation:**
- All incoming `content` values are HTML-sanitised (allowlist: `<p>`, `<a>`, `<em>`, `<strong>`, `<code>`) before storage.
- Sanitised content is served with strict CSP nonce headers.
- Raw ActivityPub JSON is stored separately from rendered content.

---

### T5 — Key Rotation Abuse

**Description:** A remote actor rotates their key and replays previously rejected activities.

**Mitigation:**
- Activity `id` is stored after processing; duplicate IDs are rejected.
- Key rotation events trigger re-validation of cached public keys.

---

### T6 — WebFinger Enumeration

**Description:** Attacker uses WebFinger to enumerate all local actor identities.

**Mitigation:**
- WebFinger only returns results for explicitly configured actors.
- No enumeration endpoint exists; per-actor lookup only.
- Rate limit on `/.well-known/webfinger` endpoint.

---

## Federation Trust Hierarchy

```
Unknown remote actor (L0)
  │
  ├─ HTTP Signature verified (Ω2) → Trusted delivery (L0+)
  ├─ Known blocked actor           → Rejected immediately
  └─ Unknown actor                 → Queued; processed after actor fetch
```

---

## Accepted Risks (with rationale)

| Risk | Accepted Because |
|------|-----------------|
| No HTTP Signature verification yet | Ω2 implementation scheduled; inbox is append-only queue, not immediately executed |
| Content moderation is manual | Automated moderation is an Ω3 feature; human review acceptable at current scale |
| No `Move` activity support | Actor migration not in scope until Ω3 |
