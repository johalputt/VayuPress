# VayuPress Troubleshooting

## Health Check Failures

### `/health` returns 500

VayuPress process is not running or crashed.

```bash
sudo systemctl status vayupress
sudo journalctl -u vayupress -n 50
```

### `/health/ready` returns 503

A required subsystem (DB, storage) is unavailable.

```bash
curl http://localhost:8080/health/dependencies
# Check which component reports "unavailable"
```

### `/health/meilisearch` returns 503

Meilisearch is down — this is non-critical. VayuPress falls back to SQLite `LIKE` queries.

```bash
sudo systemctl status meilisearch
sudo systemctl restart meilisearch
```

## Write Queue Issues

### Articles not appearing after POST

Writes are asynchronous. Check the queue:

```bash
sqlite3 /var/lib/vayupress/data.db "SELECT status, COUNT(*) FROM write_jobs GROUP BY status"
```

If jobs are stuck in `processing`:

```bash
# The stuck-job reaper runs every minute and resets jobs stuck >5 min
# Or manually reset:
sqlite3 /var/lib/vayupress/data.db "UPDATE write_jobs SET status='pending' WHERE status='processing'"
sudo systemctl restart vayupress
```

### Jobs in dead-letter queue

```bash
sqlite3 /var/lib/vayupress/data.db "SELECT id, op, dead_reason FROM write_jobs WHERE status='dead'"
```

Jobs move to `dead` after `MAX_REPLAY_COUNT` (default 3) replay attempts. Check logs for the root cause.

## Database Issues

### SQLite locked errors

WAL mode is mandatory. Verify:

```bash
sqlite3 /var/lib/vayupress/data.db "PRAGMA journal_mode"
# Expected: wal
```

If not WAL:
```bash
sqlite3 /var/lib/vayupress/data.db "PRAGMA journal_mode=WAL"
```

### Migration checksum drift (startup halted)

VayuPress detected a tampered migration. Check logs:

```bash
sudo journalctl -u vayupress | grep "CHECKSUM DRIFT"
```

This is a security alert. Do not bypass it without understanding why the checksum changed.

### Database corruption

```bash
sqlite3 /var/lib/vayupress/data.db "PRAGMA integrity_check"
# Expected: ok
```

If corrupt, restore from backup:

```bash
ls -lt /backups/vayupress-*.db.gz | head -5
# Restore:
sudo systemctl stop vayupress
gunzip -c /backups/vayupress-<date>.db.gz > /var/lib/vayupress/data.db
sudo systemctl start vayupress
```

## Cache Issues

### Stale content after update

Force-purge the cache for an article:

```bash
curl -X POST http://localhost:8080/api/v1/cache/purge \
  -H "Authorization: Bearer $API_KEY" \
  -H "X-CSRF-Token: $CSRF_TOKEN" \
  -d '{"slug": "my-article"}'
```

Or clear all cache:

```bash
sudo rm -rf /var/cache/vayupress/posts/*
```

### Cache directory unwritable

```bash
sudo chown -R www-data:www-data /var/cache/vayupress
sudo chmod -R 755 /var/cache/vayupress
```

## Performance Issues

### High memory usage

```bash
curl http://localhost:8080/metrics | grep vayu_memory
```

If idle RAM >800 MB, check for goroutine leaks:

```bash
curl http://localhost:6060/debug/pprof/goroutine?debug=1
```

### High TTFB

```bash
curl http://localhost:8080/metrics | grep vayu_http_request_duration
```

Check if Nginx is serving static files (should be, for cached pages):

```bash
nginx -t && sudo systemctl status nginx
```

## Storage Issues

### Storage quota exceeded (413 on upload)

```bash
df -h /var/www/vayupress
curl http://localhost:8080/health/storage
```

Increase `STORAGE_QUOTA_GB` in the environment, or delete old media.

### Orphaned media files

The orphan cleanup cron runs daily. To run manually:

```bash
curl -X POST http://localhost:8080/admin/storage/cleanup \
  -H "Authorization: Bearer $API_KEY"
```

## Logs

```bash
# VayuPress logs (structured JSON)
sudo journalctl -u vayupress -f

# Nginx access logs
sudo tail -f /var/log/nginx/access.log

# Meilisearch logs
sudo tail -f /var/log/meilisearch/meilisearch.log
```

## Support

support@vayupress.com — https://docs.vayupress.com
