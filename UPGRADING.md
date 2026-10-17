# Upgrading VayuPress

**How to upgrade an install** (from the console, the CLI or source), what
every release is verified against, and how to roll back:
[docs/UPGRADING.md](docs/UPGRADING.md). That guide is also served by every
install at `/docs`.

In short:

- **VayuOS → System › Updates → Update.** The release's checksum, its
  Sigstore signature from this project's release workflow, and the release
  key's Ed25519 signature are all checked before anything is written. Then the
  database is backed up, the binary is swapped with the old one kept as
  `<binary>.bak`, and the process restarts. **Roll back** is on the same page.
- **From source:** `cd /opt/vayupress && sudo git pull origin main && sudo
  ./scripts/deploy-vayupress.sh --upgrade`. Your env, data and secrets are
  kept.
- **What changed:** each version's section in [CHANGELOG.md](CHANGELOG.md).

This file covers the other half: **changing the schema**, for contributors.

---

## Schema changes and migration authoring

Migrations are numbered up/down pairs in `internal/db/migrations/`, compiled
into the binary, applied in name order at start, and checksummed:

```text
internal/db/migrations/
  110-text-repair-offers.up.sql
  110-text-repair-offers.down.sql
```

### Writing one

1. Create the next-numbered `NNN-name.up.sql` and `NNN-name.down.sql`. At
   start the binary applies each `*.up.sql` not yet in `schema_migrations`,
   then records a SHA-256 of its text there.

2. **Keep each statement on one line.** `runMigrations` (`internal/db/db.go`)
   splits the file on newlines and executes each line, so a statement wrapped
   across lines runs as fragments and fails.

   ```sql
   -- 111-article-summary.up.sql
   ALTER TABLE articles ADD COLUMN summary TEXT NOT NULL DEFAULT '';
   ```

3. **Keep it additive.** Prefer `ADD COLUMN … DEFAULT` and
   `CREATE … IF NOT EXISTS` to rewrites. A rolled-back binary then still starts
   on the migrated database, because the checksum check skips migrations it
   does not know. SQLite rewrites the whole table for some `ALTER`s, so on a
   large table that is a long write-lock.

4. **Never edit a released migration.** Its checksum is recorded on every
   install, and `verifyMigrationChecksums` halts start-up when an applied
   migration's text no longer matches (ADR-0034). To change a shipped
   migration, add a new one.

### When a migration drifts

The binary stops at start-up with `migration drift detected: <versions>` in
the journal. While the binary runs, the state is readable on the machine
itself (or with the API key):

```bash
curl -s http://localhost:8080/health/migrations
curl -s http://localhost:8080/metrics | grep vayupress_migration_drift_detected
```

### Testing one

```bash
go test ./internal/db/ -run Migrat -v     # every migration on a fresh database
go test ./...                             # then everything that reads the schema
```

---

## Getting help

- Issues: <https://github.com/johalputt/vayupress/issues>
- Security: security@vayupress.com ([SECURITY.md](SECURITY.md))
