# Attack Surfaces — VayuPress

**Status:** Authoritative  
**Last reviewed:** 2026-06-13  
**Review cycle:** Every architectural change or quarterly

---

## Surface 1 — HTTP Ingress (Nginx → Go)

| Vector | Mechanism | Mitigation |
|--------|-----------|------------|
| Path traversal | URL-encoded `../` sequences | Nginx normalises before forwarding; Go `path.Clean` in router |
| Header injection | Malformed `Host`, `X-Forwarded-For` | Nginx strips non-allowlisted headers; Go ignores hop-by-hop |
| Request smuggling | `Content-Length` / `Transfer-Encoding` conflict | Nginx configured `keepalive_requests 0` on backend |
| Slow-loris DoS | Incomplete headers held open | Nginx `client_header_timeout 10s`, `client_body_timeout 10s` |
| Large body amplification | Unbounded POST body | `client_max_body_size 8m` in Nginx; Go handler reads limited `io.LimitReader` |
| TLS downgrade | SSLv3 / TLS 1.0 | Nginx `ssl_protocols TLSv1.2 TLSv1.3` only |
| CSP bypass | Inline script injection | Nonce-based CSP (ADR-0036), no `unsafe-inline` |

---

## Surface 2 — Plugin Subprocess IPC

| Vector | Mechanism | Mitigation |
|--------|-----------|------------|
| JSON injection over stdin | Malformed plugin response escapes scanner | `bufio.Scanner` with hard byte cap; response parsed into typed struct |
| Stdout flooding | Plugin emits unbounded bytes | `io.LimitReader(stdoutPipe, maxBytes)` before scanner |
| Symlink escalation | Plugin creates symlink pointing outside scratch | `CLONE_NEWNS` + `MS_NODEV/NOSUID` on tmpfs scratch |
| Syscall escalation | Plugin calls `open("/etc/shadow")` | Seccomp-BPF allowlist; `EPERM` returned, not crash |
| Capability abuse | Plugin tries `setuid(0)` | `capset(2)` zeros all caps before plugin exec |
| FD inheritance | Parent FD leaks to child | `CloseExtraFDs` sets `CLOEXEC` on all non-stdio FDs |
| Crash loop amplification | Buggy plugin respawns endlessly | `MaxRestarts` budget; quarantine after exhaustion (ADR-0057) |
| Environment leakage | Host env vars expose secrets | `PrepareExecEnv` builds minimal allowlist; parent env not inherited |

---

## Surface 3 — SQLite Database

| Vector | Mechanism | Mitigation |
|--------|-----------|------------|
| SQL injection | Dynamic query construction | All queries use `?` placeholders; no `fmt.Sprintf` into SQL |
| WAL corruption | Crash mid-checkpoint | WAL mode; `PRAGMA integrity_check` on open; automated repair runbook |
| Migration replay | Re-running migrations | SHA-256 checksum per file; idempotent `schema_migrations` table |
| Directory traversal via DB path | `../../../etc/passwd` as DB path | Path validated against allowed prefix at startup |
| Read of stale snapshot | Dirty read under concurrent write | `BEGIN IMMEDIATE` for write transactions; reader gets consistent snapshot |

---

## Surface 4 — Federation (ActivityPub)

| Vector | Mechanism | Mitigation |
|--------|-----------|------------|
| Forged `Actor` payloads | Remote posts activity as another actor | HTTP Signature verification on all inbox payloads (planned Ω2) |
| SSRF via `object.url` | `url` field points to internal service | URL allowlist validator; private IP ranges rejected |
| Inbox flooding | High-volume `Create` activity spam | Per-actor rate limit (planned Ω2); queue depth cap |
| Content injection via `content` | ActivityPub `content` rendered as HTML | Server-side HTML sanitisation before storage |
| Key rotation attacks | Attacker rotates actor key mid-session | Key binding to DID document; rotation requires new DID |

---

## Surface 5 — AI Runtime

| Vector | Mechanism | Mitigation |
|--------|-----------|------------|
| Prompt injection via article body | Malicious content alters agent behaviour | Input sanitised before agent context; policy.Apply blocks key patterns |
| PII leakage in embeddings | Embedding encodes email/phone numbers | `DefaultPolicy` redacts PII before embedding |
| Unbounded inference time | Embedding call blocks goroutine | Context with timeout wraps all inference calls |
| Model poisoning | Local model file replaced | Executable hash verification (same mechanism as plugin binary) |

---

## Surface 6 — Signing & Archives

| Vector | Mechanism | Mitigation |
|--------|-----------|------------|
| Key compromise | Ed25519 private key stolen | Key stored outside web root; rotation via new key + re-signing |
| Signature bypass | Attacker strips `Signature` field | `Verify` called before any signed article is served |
| Hash collision | SHA-256 Merkle root collision | No known practical attack; SHA-512 upgrade path documented |
| Archive tampering | Object in archive mutated | Content-addressed storage (SHA-256 key); read-only after write |

---

## Surface 7 — DID Authentication

| Vector | Mechanism | Mitigation |
|--------|-----------|------------|
| Challenge replay | Old challenge accepted | Challenge has 5-minute TTL; one-time use enforced |
| Key substitution | Different key presented for DID | DID document binds key via `verificationMethod` |
| DID spoofing | `did:key:zAbc` crafted to match another | Ed25519 public key is the DID; no registry can be poisoned |

---

## Unmitigated / Accepted Risks

| Risk | Current Status | Planned Mitigation |
|------|---------------|-------------------|
| Seccomp filter x86-64 only | Non-Linux and non-x86 get no syscall filter | Architecture check in `buildSeccompFilter`; arm64 planned Ω2 |
| ActivityPub HTTP Signature verification | Not yet implemented | Ω2 hardening phase |
| Per-actor federation rate limits | Not yet implemented | Ω2 hardening phase |
| Mutual TLS for plugin IPC | Not implemented | Ω3 (when plugin SDK is external) |
