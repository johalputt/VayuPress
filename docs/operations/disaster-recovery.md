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

What you need: a VayuKeep generation (or a `vayupress backup` archive) from
somewhere other than the lost server, its passphrase, and a copy of
`/etc/vayupress/env`.

```bash
# 1. Provision a new Ubuntu 24.04 LTS server (8 GB RAM, 4 vCPU, 50 GB)

# 2. Install VayuPress on it, for the same domain
curl -sSL https://raw.githubusercontent.com/johalputt/vayupress/main/scripts/deploy-vayupress.sh | bash

# 3. Copy the archive across, verify it, and restore over the fresh install
scp vk-YYYYMMDD-HHMMSS.vpbk root@new-server:/root/
vayupress restore -in /root/vk-YYYYMMDD-HHMMSS.vpbk -verify
sudo systemctl stop vayupress
sudo vayupress restore -in /root/vk-YYYYMMDD-HHMMSS.vpbk -dest /var/lib/vayupress
sudo chown -R www-data:www-data /var/lib/vayupress

# 4. Put back /etc/vayupress/env (secrets, VayuKeep target, Cloudflare token)
sudo systemctl start vayupress

# 5. Point the A/AAAA records at the new server; certbot issues certificates
#    again on the next installer run or renewal

# 6. Verify
curl -s http://localhost:8080/health
```

**Expected recovery time**: 45–60 minutes, most of it DNS.

---

## DR-02: Database Corruption

**Trigger**: `SQLITE_CORRUPT` or disk I/O errors in the log, the service
failing to start on a database error, or the install in **read-only** mode.

Follow [wal-corruption-recovery.md](wal-corruption-recovery.md): stop the
service, preserve the damaged files, try a WAL checkpoint, and otherwise
restore the newest generation that verifies (DR-07).

---

## DR-03: Migration Checksum Drift (Tamper Detected)

**Trigger**: The service refuses to start with `migration drift` in the log.
At every start the server compares each applied migration's recorded SHA-256
with the migration shipped in the binary (`internal/db`), and stops if one
differs.

```bash
# 1. See which migrations drifted
sudo journalctl -u vayupress -n 50 | grep -i drift

# 2. Read what the database recorded for them
sqlite3 /var/lib/vayupress/vayupress.db \
  "SELECT version, checksum, applied_at FROM schema_migrations ORDER BY version;"
```

A shipped migration is never edited after release, so drift means the
database's record was changed, or the binary is not the one released. Treat it
as a security incident:

1. Check the binary: `go version -m /usr/local/bin/vayupress` names the module
   and version it was built from; compare it with the release's signature.
2. Change every administrator's password and revoke API keys and connector
   grants (VayuOS → Settings › Team, › API keys).
3. Restore the newest generation from **before** the drift appeared (DR-07),
   and file the incident per
   [incident-response.md](../security/incident-response.md).

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

**Trigger**: VayuOS → System › Backups shows **Restore FAILED**, or
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
sudo vayupress restore -in "$VAYUKEEP_TARGET/vk-20260727-104500.vpbk" -dest /var/lib/vayupress
sudo chown -R www-data:www-data /var/lib/vayupress

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

Every generation's database is copied through one pinned read transaction, so it is one
moment's data without stopping the service. The copy, the seal and the test restore all
go in steps, as fast as the server can spare, and slow down while visitors need the disk;
System › Backups shows the copy running and why it is going at that pace. Generations are
sealed with AES-256-GCM under an Argon2id-wrapped key, with each frame chained to the
last. Nothing is written unencrypted, and there is no option to.

**The recovery point is only as good as the last drill.** A generation nothing has read
back is a file, not a backup. Check *last verified restore* on VayuOS → System ›
Backups, not *enabled*.

Historical note: before v3.15.80 this table described a nightly 02:00 UTC job that did not
exist. The only database backup the deploy script installed ran inside `if $UPGRADE`, so
the real recovery point was "whenever you last upgraded". If you sized your risk from the
old table, re-check it.

---

See also: `docs/OPERATIONS.md` for day-to-day runbooks (RB-01 through RB-09).
