# VayuPress Scripts

This directory contains deployment and operational scripts for VayuPress.

## Scripts

### `deploy-vayupress.sh`

The primary deployment script. Installs and configures a full VayuPress stack on Ubuntu 24.04 LTS.

**Implements**: VayuPress Governance Constitution v6.0 — Prompts 1–13  
**Version**: v1.0.0-p13

**Usage**:
```bash
# Full deploy (fresh server)
sudo ./scripts/deploy-vayupress.sh

# Validate configuration without making changes
sudo ./scripts/deploy-vayupress.sh --dry-run

# Upgrade an existing installation (preserves data and secrets)
sudo ./scripts/deploy-vayupress.sh --upgrade
```

**What it installs** (in order):
1. System dependencies (nginx, sqlite3, certbot, fail2ban, ufw, Go 1.25)
2. Meilisearch (optional full-text search)
3. Isso (self-hosted comment server)
4. Self-hosted fonts — Inter + IBM Plex Mono, zero telemetry (ADR-0002)
5. VayuPress Go application (`main.go`, ~2,500 lines)
6. Systemd services (vayupress, meilisearch, isso)
7. Nginx with TLS (Let's Encrypt via Certbot)
8. UFW firewall (ports 22, 80, 443)
9. Logrotate configuration
10. Cron jobs: nightly backup, orphan cleanup, restore validation
11. Smoke tests + admin credential printout

**Governance compliance** (v1.0.0-p13):

| ADR | Description |
|-----|-------------|
| ADR-0032 | Plugin pool WaitGroup drain + context propagation (shutdown order fixed) |
| ADR-0033 | WAL adaptive checkpoint (size-triggered RESTART) |
| ADR-0034 | Migration checksum drift verification at startup |
| ADR-0035 | Dead-letter queue replay limits + poison job quarantine |
| ADR-0036 | CSP nonce centralized template helpers (unsafe-inline removed) |
| ADR-0037 | Pprof explicit handler + rate-limit + audit log |
| ADR-0038 | VACUUM cooldown + write-threshold guard |
| ADR-0039 | Deploy sourced component architecture |
| ADR-0040 | Config versioning + compatibility contracts |
| ADR-0041 | Structured health contracts with schema_version field |
| ADR-0042 | Backup restore automation + checksum registry |
| ADR-0043 | 8 new integration test files |
| ADR-0044 | Repository decomposition + source parity (cmd/vayupress/main.go) |

**Security features** (P9):
- SSRF protection: all outbound HTTP blocked for loopback, link-local (169.254.169.254), RFC-1918
- Argon2id credential hashing with constant-time comparison
- Magic-number file type verification (JPEG/PNG/GIF/WebP/PDF)
- WORM audit log: SQLite `BEFORE UPDATE`/`BEFORE DELETE` triggers raise ABORT
- CSP: `style-src 'self'` only — no `unsafe-inline`
- Bounded memory: TTL sweeper evicts stale auth/rate-limit buckets every 10 minutes

**Shutdown lifecycle** (6 phases):
1. Stop ingress (30s HTTP drain)
2. Drain write queue (45s timeout)
3. Stop plugin pool (WaitGroup + channel close)
4. WAL checkpoint (TRUNCATE)
5. Flush metrics snapshot
6. Close database

**Requirements**: Ubuntu 24.04 LTS, 8 GB RAM minimum, root/sudo access.  
**Idempotent**: Safe to run multiple times. Use `--upgrade` to preserve existing data.

### `sync-source.sh`

P13 source-parity tool. The canonical Go application source is embedded in the
`deploy-vayupress.sh` heredoc (keeping the `curl | bash` install self-contained).
This script mirrors that exact source to `cmd/vayupress/main.go` so the full Go
toolchain (build/vet/test/lint/vuln) works.

```bash
scripts/sync-source.sh           # regenerate cmd/vayupress/main.go from the heredoc
scripts/sync-source.sh --check   # CI mode: exit 1 if the two have drifted
```

The deploy script is **canonical**; the committed `cmd/vayupress/main.go` is the
mirror. CI's `P13 · Source Sync` job runs `--check` on every push. See
[ADR-0044](../docs/adr/ADR-0044-repository-decomposition.md).

## Adding New Scripts

New scripts must:
1. Be fully documented inline (purpose, usage, governance alignment)
2. Be idempotent — safe to run multiple times
3. Support `--dry-run` for validation
4. Pass `shellcheck --severity=warning`
5. Be documented in this README
