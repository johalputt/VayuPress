# VayuPress Threat Model

**Version**: 2.0.0  
**Date**: 2026-10-16 (rewritten against the code; v1.0 was 2026-06-12)  
**Author**: Security Lead, @johalputt  
**Review cycle**: Annual or after any architectural change

Every mitigation below names the file, setting or check that performs it. A
mitigation that cannot be named is listed as a residual risk instead.

---

## Trust Boundaries

```text
┌──────────────────────────────────────────────────────────────────────┐
│  INTERNET (untrusted)                                                 │
│   Readers · Members · Authors · Mail servers · MCP clients · Attackers│
│ ─────┬───────────────────────────────┬──────────────────── TB1 ───── │
│      ▼ HTTPS 443                     ▼ SMTP/IMAP/POP3 (25 587 143 993 110 995)
│  ┌──────────────────────────┐        │                                │
│  │ Nginx: TLS, compression  │        │                                │
│  └────────────┬─────────────┘        │                                │
│ ──────────────┼──────────────────────┼───────────────── TB2 ───────── │
│               ▼ 127.0.0.1:8080       ▼                                │
│  ┌────────────────────────────────────────────────────────────────┐   │
│  │ VayuPress, one Go process, user www-data                        │   │
│  │ VayuShield → routes → console / API / MCP / site / mail / talk  │   │
│  │ plugins: out of process, seccomp + namespaces                   │   │
│  └───────┬──────────────────────────────────────┬────────────────┘   │
│ ─────────┼──────────────────────────── TB3 ──────┼───────────────────  │
│          ▼                                      ▼ outbound (TB4)      │
│  /var/lib/vayupress (SQLite WAL, media,     mail delivery, ACME,       │
│  mailboxes, PGP store)                      update mirror, feeds,     │
│                                             Tor, operator-configured  │
│                                             providers                 │
└──────────────────────────────────────────────────────────────────────┘
```

- **TB1 — Internet ↔ host.** `ufw` allows 22, 80, 443 and the mail ports only.
  HTTPS terminates at nginx; mail is served by VayuPress directly over
  STARTTLS / implicit TLS.
- **TB2 — nginx ↔ application.** The application listens on `127.0.0.1:8080`.
  It resolves the client address itself and trusts forwarding headers only
  from a configured proxy (`realIPMiddleware`, `auth.ClientIP`).
- **TB3 — application ↔ storage.** One data directory owned by `www-data`;
  systemd allows writes only to the data, log, cache, static and backup
  directories (`ReadWritePaths`, `ProtectSystem=full`).
- **TB4 — application ↔ the outside.** Outbound connections are listed under
  *Network* below.

---

## Entry Points

| ID | Entry point | Authentication | Notes |
|----|-------------|----------------|-------|
| EP-01 | Public site (`/`, posts, topics, search, feeds, `llms.txt`) | None | Behind VayuShield; cached pages |
| EP-02 | Members (`/members`, sign-up, comments) | Passwordless member session | Comments are held for moderation |
| EP-03 | Console `/os` | Session cookie + TOTP (optional) + CSRF token on writes | Role checked per path (`osPathMinLevel`); `/os/api/*` defaults to admin |
| EP-04 | API `/api/v1` | API key (`X-API-Key` or `Bearer`) | Scoped `section:action` keys, stored hashed, per-key rate budget |
| EP-05 | MCP `/mcp` | OAuth 2.1 or an API key | A tool the key does not grant is invisible and refused |
| EP-06 | `/health`, `/health/live`, `/health/ready` | None | Liveness only |
| EP-07 | Detail health (`/health/db`, `/queue`, `/storage` …) and `/metrics` | Loopback or API key; 404 otherwise | Recon surface closed (audit H5) |
| EP-08 | `/debug/pprof/*` | Administrative API + its own rate limit | ADR-0037 |
| EP-09 | Payment webhooks (`/api/v1/stripe/webhook`, `/api/v1/payments/webhook/{gateway}`) | Provider signature / HMAC | Idempotent fulfilment |
| EP-10 | Mail: SMTP 25, submission 587, IMAP 143/993, POP3 110/995 | SMTP AUTH / app passwords for clients; SPF, DKIM, DMARC for inbound | `internal/vayuos/mail` |
| EP-11 | VayuTalk relay (`talk.` subdomain) | Talk token per mailbox | Messages held only in memory |
| EP-12 | `/.well-known/*` (WKD key discovery, OAuth metadata) | None | Read-only |
| EP-13 | Tor `.onion` (VayuTor) | As the clearnet routes | Onion responses carry no HSTS, `no-referrer` |
| EP-14 | SSH 22 | Operator's keys | Outside VayuPress |

---

## Assets

| ID | Asset | Sensitivity | Location |
|----|-------|-------------|----------|
| A-01 | Published posts | Public | `/var/lib/vayupress/vayupress.db` |
| A-02 | Drafts, members, orders | Confidential | The same database |
| A-03 | Install API key | Critical | `API_KEY` in `/etc/vayupress/env` |
| A-04 | Scoped API keys, OAuth grants | High | The database, as hashes |
| A-05 | TLS private keys | Critical | certbot's store (`/etc/letsencrypt`) |
| A-06 | PGP private keys | Critical | `/var/lib/vayupress/vayudata/pgp`, AES-256-GCM at rest |
| A-07 | Mailboxes | Confidential | `/var/lib/vayupress/vayudata/mail` |
| A-08 | Third-party secrets (payments, AI, DNS) | Critical | The database, AES-256-GCM at rest (`internal/secrets`) |
| A-09 | Backups | High | `VAYUKEEP_TARGET`, AES-256-GCM under an Argon2id-wrapped key |
| A-10 | Audit log | High | `audit_log`, append-only by trigger (migration 005) |
| A-11 | Media | Internal | `/var/lib/vayupress/media` |
| A-12 | Onion service keys | Critical | The database (pinned so addresses stay stable) |
| A-13 | Configuration | Critical | `/etc/vayupress/env` |

---

## Threat Actors

| ID | Actor | Motivation | Capability | Likelihood |
|----|-------|------------|------------|-----------|
| TA-01 | Script kiddie | Defacement, crypto mining | Low | High |
| TA-02 | Spam bot, scraper, AI crawler | SEO spam, comment spam, content harvesting | Low–Medium | High |
| TA-03 | Credential stuffer | Account takeover | Medium | Medium |
| TA-04 | Targeted attacker | Data exfiltration, content manipulation | High | Low |
| TA-05 | Supply chain attacker | Backdoor via dependency or release | High | Low |
| TA-06 | Malicious author or staff member | Privilege escalation, data theft | Medium | Low |
| TA-07 | Rogue maintainer | Insider threat | High | Very Low |
| TA-08 | Nation-state | Censorship, surveillance, deanonymisation of a Tor Space | Very High | Very Low |

---

## Mitigations

### Authentication & Authorization

| Threat | Mitigation | Implementation |
|--------|------------|----------------|
| Credential stuffing (TA-03) | Lockout per address | 5 failures in 15 min → locked 1 h (`internal/auth`, ADR-0021) |
| Weak password storage | Argon2id | `internal/auth` |
| Session theft | Second factor | TOTP (`internal/totp`) |
| CSRF | Double-submit token | `auth.CSRFTokenMiddleware` on every writing method |
| Privilege escalation (TA-06) | Roles per path, admin by default for the console API | `osPathMinLevel` (`cmd/vayupress/handlers_auth.go`) |
| API key theft | Scoped, hashed, expiring, revocable keys | `internal/apikeys` (ADR-0134) |
| An AI client doing too much | The connector's tools are limited to its key's scopes | `internal/mcp`, ADR-0139 |

### Injection

| Threat | Mitigation | Implementation |
|--------|------------|----------------|
| SQL injection | Values bound with `?` placeholders | `database/sql` |
| XSS through content | Post HTML sanitised to an allowlist; SVG to its own closed allowlist; a strict CSP | `bluemonday` policies in `internal/render`; scripts only by per-request nonce or hash (ADR-0036) |
| Template injection | `html/template` auto-escaping | Go standard library |
| Path traversal | Cleaned and confined paths | `render.CacheWrite`, `unsafePathComponent`, the media name pattern |
| Mail header injection | A decoded line break in a header becomes a space | `decodeMIMEWord` (`internal/vayuos/mail`) |

### Network

| Threat | Mitigation | Implementation |
|--------|------------|----------------|
| SSRF (TA-04) | Fetches of URLs a user or plugin supplies refuse private, loopback and link-local addresses | `internal/safefetch` — embeds, media import, AI providers, VayuFlow, feeds, mail recovery |
| Clickjacking | `X-Frame-Options: DENY`, `frame-ancestors 'none'` | Application headers |
| Protocol downgrade | HSTS, two years with subdomains (clearnet only) | Application headers; nginx `ssl_protocols TLSv1.2 TLSv1.3` |
| Application-layer floods, scrapers (TA-02) | VayuShield: lane, fair-shed, reputation, proof-of-work, optional XDP | `internal/vayushield` (ADR-0111) |
| Volumetric floods | **Not mitigated on the host** | See RR-04 |
| Data leaving a Tor Space (TA-08) | Clearnet callbacks gated off; egress through Tor | `internal/vayuos/torspace`, `tor_egress.go` (ADR-0141, ADR-0143) |

**Outbound connections the install makes:** mail delivery to recipients' MX
hosts; ACME (certbot); the update mirror (releases verified against a pinned
Ed25519 key); the datacenter range feeds (AWS, Google Cloud, DigitalOcean); the
Tor network when VayuTor is on; and, only when the operator configures them,
payment providers, AI providers, Cloudflare's API, IndexNow and webmentions.

### Supply Chain

| Threat | Mitigation | Implementation |
|--------|------------|----------------|
| Vulnerable dependency (TA-05) | `govulncheck` on reachable code; weekly freshness check | `ci.yml`, `dep-freshness.yml` |
| Tampered module | `go mod verify`; `go.sum` | CI |
| Build tampering | Two cold builds must be byte-identical; pinned Go toolchain | `scripts/reproducible-build.sh` |
| Artifact substitution | cosign-signed releases with an RFC 3161 timestamp; the updater verifies an Ed25519 signature against the operator's pinned key | `tag-release.yml`, `internal/update/releasekey.go` |
| Committed secrets | trufflehog | CI |
| Licence | Apache-2.0 and an SPDX identifier on every Go file | `check-licenses` |

### Data Protection

| Threat | Mitigation | Implementation |
|--------|------------|----------------|
| Backup theft | Encrypted, chained, authenticated archives | `internal/backup`, VayuKeep (ADR-0145) |
| Key theft from the database | PGP private keys and third-party secrets encrypted at rest | AES-256-GCM (`internal/vayuos/pgp`, `internal/secrets`) |
| Log tampering | Append-only audit table | Triggers in migration 005 |
| Database corruption | WAL mode; integrity checked on every staged update and every restore drill | `internal/update/snapshot.go`, `internal/vayukeep` |
| Migration tampering | Checksum verification at start; the service will not start on drift | `internal/db` (ADR-0034) |
| Reader tracking | No cookies, no IP or user agent stored; a daily-rotating salted hash | `internal/vayuanalytics/session` (ADR-0097) |

### Infrastructure

| Threat | Mitigation | Implementation |
|--------|------------|----------------|
| Root privilege abuse | Runs as `www-data`; `NoNewPrivileges`; only `CAP_NET_BIND_SERVICE` (for the mail ports) | `vayupress.service` |
| Writes outside the data directories | `ProtectSystem=full`, `ReadWritePaths` limited to data, log, cache, static and backup | `vayupress.service` |
| Port scanning | `ufw`: 22, 80, 443 and mail ports | `scripts/deploy-vayupress.sh` |
| Brute force on nginx | fail2ban jails `nginx-http-auth`, `nginx-limit-req` | `/etc/fail2ban/jail.d/vayupress.conf` |
| Plugin escape | seccomp (seven architectures), namespaces, capabilities dropped, FDs closed, minimal env | `internal/sandbox` (ADR-0057) |
| Certificate expiry | certbot's renewal timer | certbot |

---

## Residual Risks

| ID | Risk | Severity | Accepted By | Rationale |
|----|------|----------|-------------|-----------|
| RR-01 | One SQLite writer | Low | Architecture Lead | A design choice; 234,727 posts on one host serve without it binding (`docs/BENCHMARKS.md`) |
| RR-02 | ReadOnly mode does not refuse post writes | Medium | — (open) | The mode is entered on WAL write faults, but post and page writes do not consult it (`docs/architecture/system-modes.md`); tracked for a fix |
| RR-03 | SSH is outside VayuPress | Low | Operator | fail2ban's jails cover nginx, not SSH; key-only SSH is the operator's to configure |
| RR-04 | Volumetric floods | High | Security Lead | Every host layer runs after a completed handshake on the uplink; only anycast capacity in front absorbs one. VayuShield's posture report keeps it as a permanent fail |
| RR-05 | Mutual TLS for plugin IPC | Low | Security Lead | Plugins are local subprocesses; revisit when the plugin SDK is external |
| RR-06 | IndexNow trusts the configured key and host | Low | Operator | Submissions are suppressed in read-only, quarantined and maintenance modes and journalled; the key is operator-supplied and not rotated automatically |
| RR-07 | Both Spaces share one machine | Medium | Operator | Spaces separate identity and content, not the host; the console states this (ADR-0143) |

---

## Review Log

| Date | Reviewer | Changes |
|------|----------|---------|
| 2026-06-12 | @johalputt | Initial threat model (v1.0) |
| 2026-06-14 | @johalputt | Added the federation HTTP-signature gap and RR-06 (IndexNow) after a code audit |
| 2026-10-16 | @johalputt | v2.0, rewritten against the code: the isso comment server, the `vayupress` user, `/data` paths, the cron integrity check and federation are gone; entry points, assets, outbound connections and mitigations now name their mechanisms; RR-02 (read-only does not refuse post writes), RR-04 and RR-07 added |
