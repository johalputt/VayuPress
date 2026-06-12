# VayuPress Operations Runbook

**Version**: 1.0.0 (Prompt 6 — Operations)  
**Audience**: Production operators, on-call maintainers  
**SLA**: 99.9% uptime target; P1 response < 15 minutes

---

## Quick Reference

```bash
# Service status
systemctl status vayupress meilisearch isso

# Health check
curl -s http://localhost:8080/health | jq .
curl -s http://localhost:8080/health/dependencies | jq .

# Logs (last 100 lines)
journalctl -u vayupress -n 100 --no-pager

# Restart application
systemctl restart vayupress

# Emergency stop
systemctl stop vayupress
```

---

## Service Architecture

| Service | Port | User | Managed By |
|---------|------|------|-----------|
| vayupress | 8080 (localhost) | vayupress | systemd |
| nginx | 80, 443 | www-data | systemd |
| meilisearch | 7700 (localhost) | meilisearch | systemd |
| isso | 8090 (localhost) | isso | systemd |

---

## Health Endpoints

| Endpoint | Purpose | Expected Response |
|----------|---------|-------------------|
| `GET /health` | Overall health | `{"status":"ok"}` |
| `GET /health/live` | Liveness probe | `{"live":true}` |
| `GET /health/ready` | Readiness probe | `{"ready":true}` |
| `GET /health/dependencies` | Dep status | Per-service status JSON |
| `GET /health/migrations` | Migration status | Applied migration list |
| `GET /health/ethics` | Ethics compliance | `{"compliant":true}` |

---

## Runbooks

### RB-01: Application Not Starting

1. Check logs: `journalctl -u vayupress -n 50`
2. Look for `CHECKSUM DRIFT` — means a migration was tampered (ADR-0034). Do NOT restart until investigated.
3. Look for `FATAL: database locked` — kill any zombie `vayupress` processes: `pkill -f vayupress`
4. Check data dir permissions: `ls -la /data/vayupress.db`
5. Verify env vars loaded: `systemctl cat vayupress | grep EnvironmentFile`
6. Test binary directly: `sudo -u vayupress /usr/local/bin/vayupress --dry-run`

### RB-02: High Memory Usage (>800 MB)

1. Check current memory: `systemctl status vayupress | grep Memory`
2. Check for write queue backlog: `curl http://localhost:8080/health/dependencies | jq .queue`
3. Check WAL size: `ls -lh /data/vayupress.db-wal`
4. If WAL > 100 MB, checkpoint manually:
   ```bash
   sqlite3 /data/vayupress.db "PRAGMA wal_checkpoint(RESTART);"
   ```
5. If memory continues to grow, restart: `systemctl restart vayupress`
6. If recurring: check for runaway pprof goroutine, review recent deploys

### RB-03: Database Corruption

1. Run integrity check: `sqlite3 /data/vayupress.db "PRAGMA integrity_check;"`
2. If errors, IMMEDIATELY stop writes: `systemctl stop vayupress`
3. Identify most recent valid backup: `ls -lt /data/backups/*.db.gz | head -5`
4. Restore from backup (see RB-07)
5. File incident report in `docs/incidents/`

### RB-04: TLS Certificate Expired

1. Check expiry: `certbot certificates`
2. Force renewal: `certbot renew --force-renewal`
3. Test nginx config: `nginx -t`
4. Reload nginx: `systemctl reload nginx`
5. If ACME challenge fails, check firewall: `ufw status` (port 80 must be open for HTTP-01)

### RB-05: High Error Rate (>1% 5xx)

1. Check error logs: `journalctl -u vayupress | grep 'level=error' | tail -20`
2. Check `/health/dependencies` for failing deps
3. If database errors: check WAL size, run integrity check (RB-03 step 1)
4. If Meilisearch errors: `systemctl restart meilisearch` (app degrades gracefully)
5. If nginx errors: `nginx -t && systemctl reload nginx`

### RB-06: Disk Space Running Low (<20% free)

1. Check usage: `df -h /data`
2. Find large files: `du -sh /data/* | sort -rh | head -10`
3. Rotate logs: `logrotate -f /etc/logrotate.d/vayupress`
4. Clean orphaned media: `curl -X POST http://localhost:8080/api/v1/admin/cleanup-orphans -H "Authorization: Bearer $VAYU_API_KEY"`
5. Archive old backups: keep last 30 days, compress older
6. If WAL is large: checkpoint (RB-02 step 4)

### RB-07: Restore from Backup

```bash
# Stop application
systemctl stop vayupress

# List available backups
ls -lt /data/backups/

# Restore (replace TIMESTAMP with target backup)
cp /data/vayupress.db /data/vayupress.db.pre-restore
zcat /data/backups/vayupress-TIMESTAMP.db.gz > /data/vayupress.db

# Verify restored DB
sqlite3 /data/vayupress.db "PRAGMA integrity_check;"

# Set correct ownership
chown vayupress:vayupress /data/vayupress.db

# Start application
systemctl start vayupress

# Verify health
curl http://localhost:8080/health
```

### RB-08: Fail2ban Blocking Legitimate Users

1. Check if IP is banned: `fail2ban-client status nginx-limit-req`
2. Unban specific IP: `fail2ban-client set nginx-limit-req unbanip <IP>`
3. Whitelist if needed: add to `/etc/fail2ban/jail.local` under `[DEFAULT] ignoreip`
4. Reload: `fail2ban-client reload`

### RB-09: API Key Rotation

```bash
# Generate new key
NEW_KEY=$(openssl rand -hex 32)
echo "New API key: $NEW_KEY"

# Update environment
# Edit /etc/vayupress/vayupress.env
# Change VAYU_API_KEY=<new_value>

# Restart to apply
systemctl restart vayupress

# Update all API clients with new key
# Verify health
curl -H "Authorization: Bearer $NEW_KEY" http://localhost:8080/health
```

---

## Monitoring Metrics (Prometheus)

Key metrics exposed at `http://localhost:8080/metrics` (internal only):

| Metric | Alert Threshold | Action |
|--------|----------------|--------|
| `vayupress_requests_total{status="5xx"}` | >1% of total | RB-05 |
| `vayupress_memory_bytes` | >800 MB | RB-02 |
| `vayupress_wal_size_bytes` | >64 MB | RB-02 step 4 |
| `vayupress_migration_drift_detected` | >0 | RB-01 step 2 |
| `vayupress_write_queue_depth` | >1000 | RB-02 |
| `vayupress_auth_lockouts_total` | rising | Security alert |
| `vayupress_wal_checkpoint_duration_ms` | >5000 | Investigate I/O |
| `vayupress_dead_letter_queue_size` | >10 | ADR-0035 review |

---

## Scheduled Operations

| Schedule | Task | Script/Command |
|----------|------|---------------|
| Nightly 02:00 | Database backup | `/etc/cron.d/vayupress-backup` |
| Nightly 03:00 | Orphan cleanup | `POST /api/v1/admin/cleanup-orphans` |
| Weekly 04:00 | Restore validation | `/etc/cron.d/vayupress-restore-validate` |
| Daily | TLS renewal check | `certbot renew` |
| On deploy | Migration checksum verify | ADR-0034; startup check |

---

## Capacity Planning

| Resource | Current Limit | Warning Level | Action |
|----------|--------------|---------------|--------|
| Memory | 800 MB | 600 MB | Review queue depth |
| Disk (data) | Host-dependent | 80% full | Add storage or archive |
| SQLite WAL | 32 MB adaptive | 32 MB | RESTART checkpoint auto |
| Concurrent goroutines | 10,000 (default) | 8,000 | Review plugin pool |
| Binary size | 45 MB | 40 MB | `make check-size` |
| JS bundle (gzip) | 50 KB | 45 KB | Audit new dependencies |

---

## Incident Classification

| Severity | Definition | Response Time | Escalation |
|----------|-----------|--------------|-----------|
| P1 Critical | Site down, data loss, security breach | 15 min | BDFL + Security Lead |
| P2 High | Degraded performance, elevated errors | 1 hour | On-call maintainer |
| P3 Medium | Single feature broken, non-critical | 4 hours | Community Lead |
| P4 Low | Cosmetic, advisory | Next sprint | Regular backlog |

Security incidents: email security@vayupress.com immediately.

---

## Emergency Contacts

| Role | Email |
|------|-------|
| BDFL / Owner | admin@vayupress.com |
| Security Lead | security@vayupress.com |
| On-call (ops) | ops@vayupress.com |
