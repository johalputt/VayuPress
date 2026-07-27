# Disaster Recovery Runbook

**Classification**: Critical Operations  
**Owner**: Platform Maintainers  
**Review cycle**: Quarterly

---

## Severity Levels

| Level | Description | RTO | RPO |
|-------|-------------|-----|-----|
| P0 | Total service loss | 1 hour | 24 hours |
| P1 | Partial degradation | 4 hours | 24 hours |
| P2 | Performance impact | 24 hours | 48 hours |

---

## DR-01: Full Server Loss

**Trigger**: Server unreachable, disk failure, or provider incident.

```bash
# 1. Provision new Ubuntu 24.04 LTS server (8 GB RAM min)

# 2. Copy backup from offsite storage to new server
scp /path/to/vayupress-backup-YYYYMMDD.tar.gz root@new-server:/tmp/

# 3. Extract backup
tar -xzf /tmp/vayupress-backup-YYYYMMDD.tar.gz -C /var/backups/vayupress/

# 4. Run fresh deploy (restores DB from backup automatically)
git clone https://github.com/johalputt/vayupress.git
cd vayupress
sudo VAYU_RESTORE_FROM=/var/backups/vayupress/vayupress-YYYYMMDD.db \
     ./scripts/deploy-vayupress.sh

# 5. Update DNS A record to new server IP

# 6. Verify health endpoints
curl -sf https://yourdomain.com/health | jq .
curl -sf https://yourdomain.com/health/ethics | jq .
```

**Expected recovery time**: 45–60 minutes.

---

## DR-02: Database Corruption

**Trigger**: `PRAGMA integrity_check` returns errors, application refuses to start.

```bash
# 1. Stop the service
sudo systemctl stop vayupress

# 2. Verify corruption
sqlite3 /var/www/vayupress/vayupress.db "PRAGMA integrity_check;"

# 3. Attempt WAL recovery first
sqlite3 /var/www/vayupress/vayupress.db "PRAGMA wal_checkpoint(TRUNCATE);"
sqlite3 /var/www/vayupress/vayupress.db "PRAGMA integrity_check;"

# 4. If still corrupt — restore from last nightly backup
LATEST=$(ls -t /var/backups/vayupress/*.db | head -1)
sudo cp /var/www/vayupress/vayupress.db /var/www/vayupress/vayupress.db.corrupt
sudo cp "$LATEST" /var/www/vayupress/vayupress.db
sudo chown vayupress:vayupress /var/www/vayupress/vayupress.db

# 5. Verify restored DB
sqlite3 /var/www/vayupress/vayupress.db "PRAGMA integrity_check;"
sqlite3 /var/www/vayupress/vayupress.db "PRAGMA user_version;"

# 6. Restart service
sudo systemctl start vayupress
curl -sf http://localhost:8080/health/storage | jq .
```

---

## DR-03: Migration Checksum Drift (Tamper Detected)

**Trigger**: Service refuses to start with "migration checksum drift" in logs.

```bash
# 1. Check which migration is flagged
sudo journalctl -u vayupress -n 50 | grep "checksum"

# 2. Export current checksums
sqlite3 /var/www/vayupress/vayupress.db \
  "SELECT version, checksum FROM schema_migrations ORDER BY version;"

# 3. Compare against known-good checksums from CHANGELOG.md or release notes

# 4. If legitimate drift (intentional schema edit) — reset migration state:
#    WARNING: only do this if you authored the schema change
sqlite3 /var/www/vayupress/vayupress.db \
  "UPDATE schema_migrations SET checksum='<new-checksum>' WHERE version='<version>';"

# 5. If unauthorized drift — treat as security incident; rotate all credentials:
sudo /usr/local/bin/vayupress-rotate-keys.sh
# Then restore DB from last known-good backup (see DR-02)
```

---

## DR-04: TLS Certificate Expiry

**Trigger**: Certbot renewal failure, `https://` returns certificate error.

```bash
# Check certificate status
sudo certbot certificates

# Force renewal
sudo certbot renew --force-renewal --nginx

# Verify
echo | openssl s_client -connect yourdomain.com:443 2>/dev/null | \
  openssl x509 -noout -dates

# If Let's Encrypt is unavailable — install self-signed temporarily
sudo openssl req -x509 -newkey rsa:4096 -keyout /etc/ssl/private/vp-temp.key \
  -out /etc/ssl/certs/vp-temp.crt -days 30 -nodes \
  -subj "/CN=yourdomain.com"
```

---

## DR-05: Search Degraded

**Trigger**: `/health/search` returns `"status": "degraded"`. Articles still readable; search returns empty.

Search is **built in** (VayuFind, [ADR-0101](../adr/ADR-0101-builtin-search-vayufind.md)) — there is
no second service to restart. A degraded result means the index is missing or stale,
so recovery is a rebuild, not a restart.

```bash
# Confirm the engine is answering, and check the log for index errors.
curl -s http://localhost:8080/health/search
sudo journalctl -u vayupress -n 50 | grep -i search

# Rebuild the index (safe, non-destructive — articles are never touched).
curl -X POST http://localhost:8080/admin/reindex \
  -H "X-API-Key: $VAYU_API_KEY"
```

If a rebuild does not clear it, the index lives inside the VayuPress database, so
DR-01 (database restore) is the escalation — not a separate search recovery.

---

## DR-06: Backup Verification Failure

**Trigger**: VayuOS → Power & Maintenance → Backup & recovery shows **Restore FAILED**, or
`journalctl -u vayupress | grep vayukeep` contains `restore drill FAILED`.

This is an outage of your recovery path, not a warning. The generations may be present,
recent and the right size, and still not restore.

```bash
# What the engine itself reports
sudo journalctl -u vayupress -n 200 | grep vayukeep

# List generations on the target
ls -lh "$VAYUKEEP_TARGET"

# Verify one end to end without writing anything — passphrase, every
# authentication tag, the chain between frames, and the final marker
vayupress restore -in "$VAYUKEEP_TARGET/vk-YYYYMMDD-HHMMSS.vpbk" -verify
```

If `-verify` passes but the drill fails, the archive is intact and the database inside it
is not: restore it somewhere scratch and run `PRAGMA integrity_check` by hand.
If `-verify` fails, that generation is unusable — check the ones before it and treat the
target as suspect (full disk, failing mount, silent truncation on write).

---

## DR-07: Point-in-Time Restore

**Trigger**: Bad data was written and you need the site as it was before a known moment —
a bad import, a mistaken bulk edit, a compromised session.

VayuKeep keeps encrypted generations; restoring "as of" a moment selects the last
generation taken **at or before** it. It never rolls forward past the moment you asked
for, because that would hand back the data you are trying to escape.

```bash
# 1. See what you have (newest first)
ls -1 "$VAYUKEEP_TARGET" | sort -r

# 2. Verify the one you intend to use BEFORE trusting it
vayupress restore -in "$VAYUKEEP_TARGET/vk-20260727-104500.vpbk" -verify

# 3. Stop the service, then restore
sudo systemctl stop vayupress
vayupress restore -in "$VAYUKEEP_TARGET/vk-20260727-104500.vpbk" -dest /var/lib/vayupress

# 4. Start, and confirm
sudo systemctl start vayupress
curl -s http://localhost:8080/health
```

Your previous data directory is **preserved**, not deleted — the restore prints the path
it was moved to (`/var/lib/vayupress.replaced-<timestamp>`). Keep it until you have
confirmed the restored generation is the one you wanted, then remove it.

A restore that fails partway leaves the live directory untouched: entries are staged into
a sibling directory and moved into place only after the archive reaches its authenticated
final marker.

---

## Post-Incident Requirements

After any P0/P1 incident:

1. **Within 1 hour**: Incident declared in `#incidents` Slack channel
2. **Within 24 hours**: Preliminary report filed (timeline, impact, immediate fix)
3. **Within 7 days**: Full post-mortem published in `docs/post-mortems/YYYY-MM-DD-<slug>.md`
4. **Within 30 days**: Corrective actions implemented and verified

Post-mortem template: `docs/rfc-template.md` (adapt Section 4 for incident context).

---

## Backup Schedule

Backups are taken by **VayuKeep**, inside the binary — there is no cron, no timer and no
`sqlite3` CLI in the path. Point it at a target and it runs.

| What | Frequency | Retention | Location |
|------|-----------|-----------|----------|
| Everything (database, media, mailboxes, settings, PGP public material) | Adaptive: from every 5 min while writing, backing off to 6 h when idle | 24 generations **or** 30 days, whichever keeps more | `$VAYUKEEP_TARGET` |
| Restore drill | Every 12 h, and on demand | last result only | temporary, discarded |

```bash
VAYUKEEP_TARGET=/mnt/backup/vayupress   # anywhere outside the data directory
VAYU_BACKUP_PASSPHRASE=…                # the same passphrase `vayupress backup` uses
```

Setting a target IS the intent to replicate, so there is no second on/off switch to
forget. `VAYUKEEP_OFF=true` disables it explicitly.

Every generation is a `VACUUM INTO` snapshot — consistent without stopping the service —
sealed with AES-256-GCM under an Argon2id-wrapped key, with each frame chained to the
last. Nothing is written unencrypted, and there is no option to.

**The recovery point is only as good as the last drill.** A generation nothing has read
back is a file, not a backup. Check *last verified restore* on VayuOS → Power &
Maintenance, not *enabled*.

Historical note: before v3.15.80 this table described a nightly 02:00 UTC job that did not
exist. The only database backup the deploy script installed ran inside `if $UPGRADE`, so
the real recovery point was "whenever you last upgraded". If you sized your risk from the
old table, re-check it.

---

See also: `docs/OPERATIONS.md` for day-to-day runbooks (RB-01 through RB-09).
