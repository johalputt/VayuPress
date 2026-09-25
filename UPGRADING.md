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
- `004-queue-replay-fields` — Adds `replay_count` and `dead_reason` to the write queue

---

## Schema Changes & Migration Authoring

Migrations are **forward-only**, embedded into the binary, and content-checksummed.
Each lives in `internal/db/migrations/` as a numbered up/down pair:

```
internal/db/migrations/
  096-article-summary.up.sql
  096-article-summary.down.sql
```

### Authoring a new migration

1. Create the next-numbered `NNN-name.up.sql` and `NNN-name.down.sql`. On
   start the binary applies each `*.up.sql` not yet in `schema_migrations`, in
   name order, and records a SHA-256 of its text there.

2. **Keep each statement on one line.** `runMigrations` (`internal/db/db.go`)
   executes the file line by line, so a statement wrapped across lines is run
   as fragments and fails.

   ```sql
   -- 096-article-summary.up.sql
   ALTER TABLE articles ADD COLUMN summary TEXT NOT NULL DEFAULT '';
   ```

3. Keep migrations **additive**. Prefer `ADD COLUMN ... DEFAULT` and
   `CREATE ... IF NOT EXISTS` over destructive rewrites; SQLite rewrites the
   whole table for some `ALTER`s, so large tables should be migrated during a
   maintenance window.

4. **Never edit an already-released migration file.** Its checksum is recorded
   on every deployed instance, and `verifyMigrationChecksums` halts start-up
   when an applied migration's text no longer matches (ADR-0034). To change a
   shipped migration, add a *new* one.

### Automated validation

A drift stops the binary at start-up with `migration drift detected: <versions>`
in the log. The state is also readable while running:

```bash
# Applied and pending migrations, and the drift counter
curl -sf http://localhost:8080/health/migrations | jq .

# The vayupress_migration_drift_detected_total metric counts drifted versions
curl -sf http://localhost:8080/metrics | grep migration_drift
```

### Testing a migration locally

```bash
# Apply every embedded migration to a fresh database
go test ./internal/db/ -run Migrat -v

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
