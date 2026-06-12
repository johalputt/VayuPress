# VayuPress Scripts

This directory contains deployment and operational scripts for VayuPress.

## Scripts

### `deploy-vayupress.sh`

The primary deployment script. Installs and configures a full VayuPress stack on Ubuntu 24.04 LTS.

**Implements**: VayuPress Governance Constitution v6.0 — Prompts 1–12  
**Version**: v1.0.0-p12

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
1. System dependencies (nginx, sqlite3, certbot, fail2ban, ufw, Go 1.22)
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

**Governance compliance** (v1.0.0-p12):

| ADR | Description |
|-----|-------------|
| ADR-0032 | Plugin pool WaitGroup drain + context propagation |
| ADR-0033 | WAL adaptive checkpoint (size-triggered RESTART) |
| ADR-0034 | Migration checksum drift verification at startup |
| ADR-0035 | Dead-letter queue replay limits + poison job quarantine |
| ADR-0036 | CSP nonce centralized template helpers |
| ADR-0037 | Pprof explicit handler + rate-limit + audit log |
| ADR-0038 | VACUUM cooldown + write-threshold guard |
| ADR-0039 | Deploy sourced component architecture |
| ADR-0040 | Config versioning + compatibility contracts |
| ADR-0041 | Structured health contracts (/health/dependencies etc.) |
| ADR-0042 | Backup restore automation + checksum registry |
| ADR-0043 | 8 new integration test files |

**Requirements**: Ubuntu 24.04 LTS, 8 GB RAM minimum, root/sudo access.  
**Idempotent**: Safe to run multiple times. Use `--upgrade` to preserve existing data.

## Adding New Scripts

New scripts must:
1. Be fully documented inline (purpose, usage, governance alignment)
2. Be idempotent — safe to run multiple times
3. Support `--dry-run` for validation
4. Pass `shellcheck --severity=warning`
5. Be documented in this README
