# Upgrading VayuPress

This document describes how to upgrade VayuPress between versions safely.

---

## General Upgrade Procedure

```bash
# 1. Back up your data first
sudo -u vayupress /usr/local/bin/vayupress-backup.sh

# 2. Pull the latest release
cd /opt/vayupress
sudo git fetch origin main && sudo git merge origin/main

# 3. Run the upgrade (preserves data and secrets)
sudo ./scripts/deploy-vayupress.sh --upgrade

# 4. Verify the service is healthy
curl -sf http://localhost:8080/health | jq .
curl -sf http://localhost:8080/health/ethics | jq .
```

The `--upgrade` flag:
- Preserves the SQLite database, API keys, and TLS certificates
- Rebuilds and reinstalls the Go binary
- Runs forward-only database migrations
- Reloads systemd services

---

## Version-Specific Notes

### → v1.0.0-p12

**New environment variables** (all optional, have defaults):

| Variable | Default | Description |
|----------|---------|-------------|
| `VAYU_ETHICS_CONTACT` | `ethics@vayupress.com` | Ethics review board contact |

**Database migrations applied automatically:**
- `005-audit-log-worm` — Adds immutable audit log table with ABORT triggers

**Deprecations:**
- `QUEUE_MAX_RETRIES` — Renamed to `MAX_REPLAY_COUNT`. Old name still accepted with a warning.
- `ConfigVersion=1.0` validation active — incompatible config keys log a warning at startup.

**Removed:** None.

### → v1.0.0-p8

**New environment variables:**

| Variable | Default | Description |
|----------|---------|-------------|
| `MAX_REPLAY_COUNT` | `100` | Dead-letter queue max replays before quarantine |
| `CONFIG_VERSION` | `1.0` | Config version for compatibility validation |

**Database migrations applied automatically:**
- `004-plugin-pool` — Plugin pool tracking table

---

## Rollback Procedure

VayuPress does not support automatic rollback of database migrations. To roll back:

1. Restore from the pre-upgrade backup:
   ```bash
   sudo systemctl stop vayupress
   sudo cp /var/backups/vayupress/vayupress-YYYYMMDD.db /var/www/vayupress/vayupress.db
   ```
2. Reinstall the previous binary version from the prior release tag.
3. Start the service: `sudo systemctl start vayupress`

See `docs/operations/disaster-recovery.md` for full restore procedures.

---

## Zero-Downtime Upgrades

VayuPress uses a single Go binary + SQLite, so zero-downtime upgrades use the OS swap:

```bash
# Build new binary alongside running service
cd /var/www/vayupress/src
sudo go build -o /usr/local/bin/vayupress.new .

# Atomically replace binary and reload
sudo mv /usr/local/bin/vayupress.new /usr/local/bin/vayupress
sudo systemctl reload vayupress   # sends SIGUSR2 for graceful restart
```

Nginx continues serving traffic during the reload. In-flight requests complete before the old process exits (30-second drain window).

---

## Verifying an Upgrade

After any upgrade, run the full health suite:

```bash
curl -sf https://yourdomain.com/health              | jq .
curl -sf https://yourdomain.com/health/dependencies | jq .
curl -sf https://yourdomain.com/health/storage      | jq .
curl -sf https://yourdomain.com/health/queue        | jq .
curl -sf https://yourdomain.com/health/search       | jq .
curl -sf https://yourdomain.com/health/ethics       | jq .
```

All endpoints must return `"status": "ok"`.

---

## Getting Help

- Open a GitHub issue: https://github.com/johalputt/vayupress/issues
- Security issues: security@vayupress.com (see SECURITY.md)
