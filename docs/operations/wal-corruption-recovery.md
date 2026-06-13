# WAL Corruption Recovery — VayuPress Operations

**Severity:** P0  
**Last reviewed:** 2026-06-13

---

## Detection

SQLite WAL corruption typically manifests as:

```
SQLITE_CORRUPT: database disk image is malformed
SQLITE_IOERR_READ: disk I/O error
PRAGMA integrity_check → returns rows (not "ok")
```

VayuPress runs `PRAGMA integrity_check` at startup (in `internal/db/config.go`).  
A failed check will cause the process to log `level=error, component=db` and may prevent startup.

---

## Immediate Actions (P0 — < 1 hour)

```bash
# 1. Stop the service immediately to prevent further writes
systemctl stop vayupress

# 2. Preserve the corrupt DB for forensics
BACKUP_TS=$(date +%Y%m%d-%H%M%S)
cp -a /data/vayupress.db      /tmp/corrupt-db-${BACKUP_TS}.db
cp -a /data/vayupress.db-wal  /tmp/corrupt-db-${BACKUP_TS}.db-wal
cp -a /data/vayupress.db-shm  /tmp/corrupt-db-${BACKUP_TS}.db-shm

# 3. Attempt WAL checkpoint recovery
sqlite3 /data/vayupress.db "PRAGMA wal_checkpoint(TRUNCATE);"
sqlite3 /data/vayupress.db "PRAGMA integrity_check;" | head -20
# If "ok" → WAL recovered; proceed to step 5
# If still errors → proceed to step 4
```

## Recovery from Backup (if WAL recovery fails)

```bash
# 4a. Identify most recent clean backup
ls -lt /backup/vayupress/ | head -10

# 4b. Restore from backup
CLEAN_BACKUP=$(ls -t /backup/vayupress/*.db | head -1)
cp "${CLEAN_BACKUP}" /data/vayupress.db
rm -f /data/vayupress.db-wal /data/vayupress.db-shm

# 4c. Verify restored DB
sqlite3 /data/vayupress.db "PRAGMA integrity_check;"
sqlite3 /data/vayupress.db "SELECT COUNT(*) FROM articles;"

# 5. Restart service
systemctl start vayupress
journalctl -u vayupress -f  # watch for errors
```

## If No Clean Backup Exists

```bash
# Attempt salvage: dump all recoverable data
sqlite3 /data/vayupress.db ".recover" > /tmp/recovered.sql 2>&1

# Create fresh DB and replay
sqlite3 /data/vayupress-new.db < /tmp/recovered.sql
sqlite3 /data/vayupress-new.db "PRAGMA integrity_check;"

# If clean, replace:
mv /data/vayupress.db /tmp/corrupt-final.db
mv /data/vayupress-new.db /data/vayupress.db
systemctl start vayupress
```

---

## Post-Incident

1. Determine data loss window (compare `MAX(created_at)` in restored DB vs backup timestamp).
2. Re-apply missing events from outbox if any were in-flight.
3. Run `vayupress verify-all-articles` to check signing integrity.
4. File P0 incident report per [`docs/security/incident-response.md`](../security/incident-response.md).
5. Review backup frequency — consider reducing from daily to hourly WAL shipping.
