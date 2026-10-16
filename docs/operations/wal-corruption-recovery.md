# Database Corruption Recovery — VayuPress Operations

**Severity:** P0  
**Last reviewed:** 2026-10-16

The database is one SQLite file in WAL mode, `/var/lib/vayupress/vayupress.db`
by default (`DB_PATH`), with its `-wal` and `-shm` beside it.

---

## Detection

Corruption shows as one of:

```text
SQLITE_CORRUPT: database disk image is malformed
SQLITE_IOERR_READ: disk I/O error
```

in `journalctl -u vayupress`, as the service failing to start with a database
error, or as the install entering **read-only** mode (writes refused), which the
console's system bar shows on every page.

The server does **not** run `PRAGMA integrity_check` when it opens the database:
on a large database that would hold the listener for minutes. The check runs on
every staged update before it is applied, and in every VayuKeep restore drill.
To check the live file yourself, stop the service first.

---

## Immediate actions (P0 — under an hour)

```bash
DB=/var/lib/vayupress/vayupress.db

# 1. Stop the service so nothing writes while you work
sudo systemctl stop vayupress

# 2. Preserve the damaged files for forensics
TS=$(date +%Y%m%d-%H%M%S)
sudo cp -a "$DB" "/root/corrupt-$TS.db"
sudo cp -a "$DB-wal" "/root/corrupt-$TS.db-wal" 2>/dev/null || true

# 3. Check, and try folding the WAL back in
sqlite3 "$DB" "PRAGMA integrity_check;" | head -20
sqlite3 "$DB" "PRAGMA wal_checkpoint(TRUNCATE);"
sqlite3 "$DB" "PRAGMA integrity_check;" | head -20
# "ok" → recovered: start the service (step 5 below).
```

`sqlite3` is not part of VayuPress; install it with `apt install sqlite3` if
the host lacks it.

## Restore from a backup (if the checkpoint does not recover it)

Restore the newest generation that **verifies** — see
[backup-restore.md](backup-restore.md):

```bash
vayupress restore -in /mnt/backup/vayupress/vk-YYYYMMDD-HHMMSS.vpbk -verify
sudo systemctl stop vayupress
sudo vayupress restore -in /mnt/backup/vayupress/vk-YYYYMMDD-HHMMSS.vpbk -dest /var/lib/vayupress
sudo chown -R www-data:www-data /var/lib/vayupress   # the service runs as www-data

# 5. Start and confirm
sudo systemctl start vayupress
curl -s http://localhost:8080/health
journalctl -u vayupress -f
```

## If no backup verifies

Salvage what SQLite can still read into a fresh file:

```bash
sqlite3 "$DB" ".recover" > /root/recovered.sql
sqlite3 /root/vayupress-new.db < /root/recovered.sql
sqlite3 /root/vayupress-new.db "PRAGMA integrity_check;"

# If it reports ok, put it in place
sudo mv "$DB" "/root/corrupt-final-$TS.db"
sudo rm -f "$DB-wal" "$DB-shm"
sudo install -o www-data -g www-data -m 600 /root/vayupress-new.db "$DB"
sudo systemctl start vayupress
```

On start the server runs its migrations and verifies their checksums; a
salvaged file missing schema it needs will say so in the log rather than serve
from it.

---

## Afterwards

1. Work out the loss window: the newest `created_at` in the restored database
   against the generation's time.
2. Check VayuOS → System › Backups: if no generation verified, fix the
   recovery path before anything else (DR-06 in
   [disaster-recovery.md](disaster-recovery.md)).
3. File the incident per [incident-response.md](../security/incident-response.md).
4. Look at the disk: corruption without a power loss is most often a failing
   volume or a full disk.
