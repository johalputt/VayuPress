# VayuPress Threat Model

**Version**: 1.0.0  
**Date**: 2026-06-12  
**Author**: Security Lead, @johalputt  
**Review cycle**: Annual or after any architectural change

---

## Trust Boundaries

```
┌─────────────────────────────────────────────────────────────┐
│  INTERNET (untrusted)                                        │
│  ┌──────────┐  ┌──────────┐  ┌────────────┐               │
│  │ Readers  │  │ Authors  │  │ Attackers  │               │
│  └────┬─────┘  └────┬─────┘  └─────┬──────┘               │
│       │              │               │                       │
│  ─────┼──────────────┼───────────────┼────── TrustBoundary1 │
│       ▼              ▼               ▼                       │
│  ┌─────────────────────────────────────────┐               │
│  │  Nginx (TLS termination + WAF headers)  │               │
│  └────────────────────┬────────────────────┘               │
│  ──────────────────────┼────────────────── TrustBoundary2   │
│                        ▼                                     │
│  ┌─────────────────────────────────────────┐               │
│  │  VayuPress Go binary (localhost:8080)   │               │
│  │  - Single process, non-root uid         │               │
│  │  - Read-only filesystem except /data    │               │
│  └───────┬───────────────────┬─────────────┘               │
│  ─────────┼───────────────────┼──────── TrustBoundary3      │
│           ▼                   ▼                              │
│  ┌──────────────┐   ┌──────────────────┐                   │
│  │ SQLite /data │   │ Meilisearch      │                   │
│  │ (rw, WAL)    │   │ localhost:7700   │                   │
│  └──────────────┘   └──────────────────┘                   │
└─────────────────────────────────────────────────────────────┘
```

**TB1** — Internet ↔ Nginx: TLS required, rate limiting via fail2ban, CSP/HSTS enforced  
**TB2** — Nginx ↔ App: Unix socket or 127.0.0.1 only, no direct public access  
**TB3** — App ↔ Storage: Filesystem ACLs, SQLite WAL, no network exposure of DB

---

## Entry Points

| ID | Entry Point | Protocol | Auth Required | Notes |
|----|-------------|----------|---------------|-------|
| EP-01 | `GET /` (public posts) | HTTPS | No | Rate-limited by nginx |
| EP-02 | `POST /api/v1/posts` | HTTPS | API key (Bearer) | Authors only |
| EP-03 | `POST /api/v1/admin/*` | HTTPS | API key + lockout | Admin actions |
| EP-04 | `GET /health/*` | HTTPS | No (read-only) | Health contracts |
| EP-05 | `GET /metrics` | localhost only | No | Prometheus; not exposed publicly |
| EP-06 | `GET /debug/pprof/*` | HTTPS | API key + rate limit | Audit logged |
| EP-07 | `POST /webhooks/github` | HTTPS | HMAC-SHA256 | Signature verified |
| EP-08 | SSH (port 22) | SSH | Key auth only | Admin server access |
| EP-09 | `GET/POST /isso/*` | HTTPS | CSRF token | Comment system proxy |
| EP-10 | Meilisearch API | localhost only | Master key | Never exposed publicly |

---

## Assets

| ID | Asset | Sensitivity | Location | Owner |
|----|-------|-------------|----------|-------|
| A-01 | Published posts | Public | SQLite `/data/vayupress.db` | VayuPress |
| A-02 | Draft posts | Confidential | SQLite, author-only | Authors |
| A-03 | Admin API key | Critical | Env var `VAYU_API_KEY` | Operator |
| A-04 | Meilisearch master key | Critical | Env var `VAYU_MEILI_KEY` | Operator |
| A-05 | TLS private key | Critical | `/etc/letsencrypt/live/` | Certbot |
| A-06 | SQLite database | High | `/data/vayupress.db` | VayuPress |
| A-07 | Backup archives | High | `/data/backups/` | VayuPress |
| A-08 | Audit logs | High | `audit_log` table (WORM) | VayuPress |
| A-09 | User comments | Internal | Isso SQLite | VayuPress |
| A-10 | Search index | Internal | Meilisearch data dir | VayuPress |
| A-11 | Media uploads | Internal | `/data/media/` | VayuPress |
| A-12 | Config / env vars | Critical | `/etc/vayupress/` | Operator |

---

## Threat Actors

| ID | Actor | Motivation | Capability | Likelihood |
|----|-------|------------|------------|-----------|
| TA-01 | Script kiddie | Defacement, crypto mining | Low | High |
| TA-02 | Spam bot | SEO spam, comment spam | Low-Medium | High |
| TA-03 | Credential stuffer | Account takeover | Medium | Medium |
| TA-04 | Targeted attacker | Data exfiltration, content manipulation | High | Low |
| TA-05 | Supply chain attacker | Backdoor via dependency | High | Low |
| TA-06 | Malicious author | Privilege escalation, data theft | Medium | Low |
| TA-07 | Rogue maintainer | Insider threat | High | Very Low |
| TA-08 | Nation-state | Censorship, surveillance | Very High | Very Low |

---

## Mitigations

### Authentication & Authorization

| Threat | Mitigation | Implementation |
|--------|------------|----------------|
| Credential stuffing (TA-03) | Account lockout after 10 failures | `authFail` counter, 15-min lockout |
| API key theft | Key rotation endpoint | `POST /api/v1/admin/rotate-key` |
| Weak password hashing | Argon2id (not bcrypt) | `argon2.IDKey()` in Go |
| Privilege escalation (TA-06) | Role-based: author vs admin | Separate API key scopes |
| Session fixation | Bearer tokens, no sessions | Stateless JWT-style API keys |

### Injection Attacks

| Threat | Mitigation | Implementation |
|--------|------------|----------------|
| SQL injection | Parameterized queries only | `database/sql` with `?` placeholders |
| XSS via content | CSP nonce per request | ADR-0036; `Content-Security-Policy: nonce-{uuid}` |
| SSTI (template injection) | html/template (auto-escape) | Go stdlib; no raw string templates |
| Command injection | No shell exec in hot path | `exec.Command` only in deploy script |
| Path traversal (media) | `filepath.Clean` + prefix check | Media handler validates path within `/data/media/` |

### Network Attacks

| Threat | Mitigation | Implementation |
|--------|------------|----------------|
| SSRF (TA-04) | Block link metadata fetches to private IPs | 169.254.169.254, 10.x, 172.16.x, 192.168.x blocked |
| CSRF | Per-request CSRF token | `X-CSRF-Token` header checked on state-changing requests |
| Clickjacking | X-Frame-Options: DENY | Nginx header |
| Protocol downgrade | HSTS (2yr, includeSubDomains) | `Strict-Transport-Security: max-age=63072000` |
| DDoS | Nginx rate limiting + fail2ban | 10 req/s per IP, ban after 20 failures |

### Supply Chain

| Threat | Mitigation | Implementation |
|--------|------------|----------------|
| Dependency backdoor (TA-05) | `govulncheck` in CI | Scans all transitive deps |
| Build tampering | Reproducible builds | `CGO_ENABLED=0 go build` |
| Artifact substitution | SHA-256 checksums in CHANGELOG | Verifiable release artifacts |
| Dependency drift | `go mod verify` in CI | Ensures go.sum integrity |
| License violation | `go-licenses check` in CI | All deps must be approved licenses |

### Data Protection

| Threat | Mitigation | Implementation |
|--------|------------|----------------|
| Data exfiltration (TA-04) | No external network calls from app | Outbound blocked except Let's Encrypt ACME |
| Backup theft | Encrypted offsite backups | AES-256 (operator-configured) |
| Log tampering | WORM audit table | `audit_log` insert-only, no UPDATE/DELETE grants |
| Database corruption | WAL mode + nightly integrity check | `PRAGMA integrity_check` in cron |
| Migration tampering | Checksum drift detection | ADR-0034; halts startup if drift found |

### Infrastructure

| Threat | Mitigation | Implementation |
|--------|------------|----------------|
| Root privilege abuse | VayuPress runs as `vayupress` uid | systemd `User=vayupress` |
| File write outside data dir | Read-only filesystem | systemd `ProtectSystem=strict`, `ReadWritePaths=/data` |
| Port scanning | UFW default deny | Ports 22, 80, 443 only |
| SSH brute force (TA-01) | fail2ban ssh jail | 5 failures → 10-min ban |
| Certificate expiry | Auto-renew via certbot cron | Daily certbot renew check |

---

## Residual Risks

| ID | Risk | Severity | Accepted By | Rationale |
|----|------|----------|-------------|-----------|
| RR-01 | Single SQLite writer is a bottleneck | Low | Architecture Lead | Design choice; escape hatch to PG exists |
| RR-02 | Isso comment system is 3rd-party Go | Low | Security Lead | Isolated process, no DB access |
| RR-03 | Let's Encrypt outbound ACME traffic | Low | Security Lead | Required for TLS; ACME pinned to LE CAs |
| RR-04 | Meilisearch has no auth in dev mode | Medium | Operator | Must set master key in production |

---

## Review Log

| Date | Reviewer | Changes |
|------|----------|---------|
| 2026-06-12 | @johalputt | Initial threat model (v1.0) |
