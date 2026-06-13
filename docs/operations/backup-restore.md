# Backup & Restore — VayuPress Operations

**Last reviewed:** 2026-06-13

---

## What Must Be Backed Up

| Item | Location | Frequency | Method |
|------|----------|-----------|--------|
| SQLite DB | `/data/vayupress.db` | Daily + WAL shipping | SQLite Online Backup API |
| Signing private key | `/etc/vayupress/signing.key` | On creation/rotation | Encrypted cold storage |
| Plugin binaries | `/opt/vayupress/plugins/` | On install/update | File copy |
| Nginx config | `/etc/nginx/conf.d/vayupress.conf` | On change | Git-tracked |
| Systemd unit | `/etc/systemd/system/vayupress.service` | On change | Git-tracked |
| TLS certificate | Managed by certbot | Auto-renewed | certbot backup |
| Archive objects | `/data/archives/` | Continuous | Content-addressed; immutable |

---

## Automated Backup (Daily)

The `deploy/backup.sh` script (or systemd timer) performs:

```bash
#!/usr/bin/env bash
set -euo pipefail
DEST="/backup/vayupress/$(date +%Y%m%d-%H%M%S)"
mkdir -p "${DEST}"

# SQLite online backup (safe while DB is live)
sqlite3 /data/vayupress.db ".backup '${DEST}/vayupress.db'"

# Plugin binaries
cp -a /opt/vayupress/plugins/ "${DEST}/plugins/"

# Verify backup integrity
sqlite3 "${DEST}/vayupress.db" "PRAGMA integrity_check;" | grep -q "^ok$"

# Rotate: keep last 30 days
find /backup/vayupress -maxdepth 1 -type d -mtime +30 -exec rm -rf {} +

echo "Backup complete: ${DEST}"
```

Schedule via systemd timer:

```ini
# /etc/systemd/system/vayupress-backup.timer
[Timer]
OnCalendar=*-*-* 02:00:00
Persistent=true
```

---

## Restore Procedure

```bash
# 1. Stop service
systemctl stop vayupress

# 2. Choose backup
BACKUP_DIR=$(ls -td /backup/vayupress/*/ | head -1)
echo "Restoring from: ${BACKUP_DIR}"

# 3. Verify backup before restore
sqlite3 "${BACKUP_DIR}/vayupress.db" "PRAGMA integrity_check;" | grep -q "^ok$"

# 4. Restore DB
cp "${BACKUP_DIR}/vayupress.db" /data/vayupress.db
rm -f /data/vayupress.db-wal /data/vayupress.db-shm

# 5. Restore plugins if needed
cp -a "${BACKUP_DIR}/plugins/" /opt/vayupress/plugins/

# 6. Verify signing key is present (not backed up here — comes from cold storage)
test -f /etc/vayupress/signing.key || echo "WARNING: signing key missing — restore from cold storage"

# 7. Start service
systemctl start vayupress
journalctl -u vayupress -f

# 8. Smoke test
curl -sf http://localhost:8080/healthz | jq .
```

---

## Restore Verification Checklist

After every restore (planned or emergency):

- [ ] `PRAGMA integrity_check` returns `ok`
- [ ] Article count matches expected (compare with monitoring baseline)
- [ ] `/healthz` returns 200
- [ ] At least one article renders correctly with valid signature
- [ ] Plugin subprocess starts (check logs for `started plugin subprocess`)
- [ ] Metrics endpoint responds: `curl http://localhost:8080/metrics`
- [ ] No `level=error` entries in first 5 minutes of logs

---

## Signing Key — Cold Storage Procedure

The Ed25519 signing key is **not** stored in the automated backup.  
It must be stored separately in encrypted cold storage (e.g., age-encrypted file on offline media).

```bash
# Encrypt key for cold storage (requires age)
age -r <recipient-public-key> /etc/vayupress/signing.key > signing.key.age

# Decrypt for restore
age -d -i <recipient-private-key> signing.key.age > /etc/vayupress/signing.key
chmod 600 /etc/vayupress/signing.key
chown vayupress: /etc/vayupress/signing.key
```
