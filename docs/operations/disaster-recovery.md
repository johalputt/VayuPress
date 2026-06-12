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

## DR-05: Search Service Down (Meilisearch)

**Trigger**: `/health/search` returns `"status": "degraded"`. Articles still readable; search returns empty.

```bash
# Check Meilisearch status
sudo systemctl status meilisearch
sudo journalctl -u meilisearch -n 50

# Restart if crashed
sudo systemctl restart meilisearch

# Re-index all articles (safe, non-destructive)
curl -X POST http://localhost:8080/admin/reindex \
  -H "X-API-Key: $VAYU_API_KEY"

# If Meilisearch data dir is corrupt — wipe and re-index
sudo systemctl stop meilisearch
sudo rm -rf /var/lib/meilisearch/data.ms
sudo systemctl start meilisearch
curl -X POST http://localhost:8080/admin/reindex \
  -H "X-API-Key: $VAYU_API_KEY"
```

---

## DR-06: Backup Verification Failure

**Trigger**: Nightly restore validation cron fails (`/var/log/vayupress-backup.log` contains FAIL).

```bash
# Check backup log
tail -50 /var/log/vayupress-backup.log

# List available backups and sizes
ls -lh /var/backups/vayupress/

# Manual restore test (non-destructive — restores to temp location)
sqlite3 /var/backups/vayupress/vayupress-YYYYMMDD.db "PRAGMA integrity_check;"

# Verify checksum against registry
sha256sum /var/backups/vayupress/vayupress-YYYYMMDD.db
cat /var/backups/vayupress/checksums.txt | grep YYYYMMDD
```

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

| Backup | Frequency | Retention | Location |
|--------|-----------|-----------|----------|
| Full DB | Nightly 02:00 UTC | 30 days | `/var/backups/vayupress/` |
| Config | On change | 10 versions | `/var/backups/vayupress/config/` |
| Media | Weekly | 90 days | `/var/backups/vayupress/media/` |

Offsite replication: configure `VAYU_BACKUP_REMOTE` env var (rsync target or S3-compatible URL).

---

See also: `docs/OPERATIONS.md` for day-to-day runbooks (RB-01 through RB-09).
