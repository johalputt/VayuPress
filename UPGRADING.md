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

## Schema Changes & Migration Authoring

Migrations are **forward-only**, embedded into the binary, and content-checksummed.
Each lives in `internal/migrations/sql/` as a numbered up/down pair:

```
internal/migrations/sql/
  010_article_summary.up.sql
  010_article_summary.down.sql
```

### Authoring a new migration

1. Create the next-numbered `NNN_name.up.sql` and `NNN_name.down.sql`. The
   engine applies `*.up.sql` in numeric order exactly once and records a
   SHA-256 of each file in the `schema_migrations` table.

   ```sql
   -- 010_article_summary.up.sql
   ALTER TABLE articles ADD COLUMN summary TEXT NOT NULL DEFAULT '';
   ```

   ```sql
   -- 010_article_summary.down.sql  (documents intent; not auto-run)
   ALTER TABLE articles DROP COLUMN summary;
   ```

2. Keep migrations **additive and idempotent-friendly**. Prefer
   `ADD COLUMN ... DEFAULT` over destructive rewrites; SQLite rewrites the whole
   table for some `ALTER`s, so large tables should be migrated during a
   maintenance window.

3. **Never edit an already-released migration file.** Its checksum is recorded
   on every deployed instance; changing the bytes trips drift detection (below)
   and the instance refuses to treat the schema as trusted. To change a shipped
   migration, add a *new* one.

### Automated validation

Checksum drift is detected automatically and surfaced two ways:

```bash
# Liveness contract — fails (non-ok) if any applied migration's bytes changed
curl -sf http://localhost:8080/health/migrations | jq .

# The vayupress_migration_drift_detected_total metric increments on drift
curl -sf http://localhost:8080/metrics | grep migration_drift
```

`internal/migrations` (`VerifyChecksums`) compares each row in
`schema_migrations` against the embedded SQL and returns the drifting versions;
the deploy script and the `/health/migrations` contract both call into it, so a
bad upgrade is caught before it serves traffic.

### Testing a migration locally

```bash
# Apply against a throwaway DB and confirm the schema + checksums verify
DB_PATH=$(mktemp -u).db go test ./internal/migrations/ -run TestMigrate -v

# Full gate before pushing a schema change
gofmt -l . && go vet ./... && go test ./...
```

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
