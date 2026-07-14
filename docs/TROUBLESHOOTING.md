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
sqlite3 /var/lib/vayupress/vayupress.db "SELECT status, COUNT(*) FROM write_jobs GROUP BY status"
```

If jobs are stuck in `processing`:

```bash
# The stuck-job reaper runs every minute and resets jobs stuck >5 min
# Or manually reset:
sqlite3 /var/lib/vayupress/vayupress.db "UPDATE write_jobs SET status='pending' WHERE status='processing'"
sudo systemctl restart vayupress
```

### Jobs in dead-letter queue

```bash
sqlite3 /var/lib/vayupress/vayupress.db "SELECT id, op, dead_reason FROM write_jobs WHERE status='dead'"
```

Jobs move to `dead` after `MAX_REPLAY_COUNT` (default 3) replay attempts. Check logs for the root cause.

## Database Issues

### SQLite locked errors

WAL mode is mandatory. Verify:

```bash
sqlite3 /var/lib/vayupress/vayupress.db "PRAGMA journal_mode"
# Expected: wal
```

If not WAL:
```bash
sqlite3 /var/lib/vayupress/vayupress.db "PRAGMA journal_mode=WAL"
```

### Migration checksum drift (startup halted)

VayuPress detected a tampered migration. Check logs:

```bash
sudo journalctl -u vayupress | grep "CHECKSUM DRIFT"
```

This is a security alert. Do not bypass it without understanding why the checksum changed.

### Database corruption

```bash
sqlite3 /var/lib/vayupress/vayupress.db "PRAGMA integrity_check"
# Expected: ok
```

If corrupt, restore from backup:

```bash
ls -lt /backups/vayupress-*.db.gz | head -5
# Restore:
sudo systemctl stop vayupress
gunzip -c /backups/vayupress-<date>.db.gz > /var/lib/vayupress/vayupress.db
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

## VayuTalk Chat Issues

### Messages queue / never deliver live; web shows "Reconnecting…"; the mobile app receives nothing

VayuTalk delivers messages and read receipts over a long-lived **Server-Sent
Events** stream (`GET /api/v1/talk/stream` for the app, `GET /os/talk/stream`
for the web console). If that stream can't stay open, messages only ever drain
from the queue during brief connect windows — so live delivery and read
receipts silently fail, the web status dot sticks on "Reconnecting…", and the
app (which can send via POST but can't hold the stream) appears to receive
nothing. Two layers in front of VayuPress commonly break the stream:

**1. Reverse proxy (nginx) buffering / idle timeout.** A generic proxy block
buffers the response and times the connection out (nginx defaults:
`proxy_read_timeout 60s`). The SSE endpoints need dedicated `location` blocks:

```nginx
    location = /api/v1/talk/stream {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_buffering off; proxy_cache off; gzip off;
        chunked_transfer_encoding on;
        proxy_read_timeout 3600s; proxy_send_timeout 3600s;
    }
    location = /os/talk/stream {
        # …same, plus:  proxy_pass_header X-CSRF-Token;
    }
```

`deploy-vayupress.sh` writes these automatically; an existing server needs them
added and `nginx -s reload`. Verify with `curl -N https://DOMAIN/api/v1/talk/stream`
— you should get a JSON `invalid-token` error (or an `event: ping`), NOT an
immediate disconnect.

**2. A CDN / bot challenge in front of nginx (e.g. Cloudflare).** A browser can
solve a JavaScript "managed challenge," so the **web** may work — but the mobile
app is a plain HTTP client that **cannot** solve it, so Cloudflare returns a
`Just a moment…` HTML page instead of the event stream and the app's stream never
connects. Diagnose it:

```bash
curl -N -s https://DOMAIN/api/v1/talk/stream -H 'User-Agent: Go-http-client/2.0' | head -c 300
```

If you see `<!DOCTYPE html>… Just a moment…` (look for `cf-ray`,
`challenges.cloudflare.com`), the CDN is intercepting the stream. **Do not turn
bot protection off site-wide** — exempt only the VayuTalk paths. In Cloudflare:
**Security → WAF → Custom rules → Create rule**, expression:

```
(starts_with(http.request.uri.path, "/api/v1/")) or (starts_with(http.request.uri.path, "/os/talk/"))
```

Action **Skip** → skip managed rules, Bot Fight Mode, and (if shown) Security
Level / Browser Integrity Check. The rest of the site keeps full CDN protection;
only the VayuTalk API/stream paths pass straight through. When fixed, the `curl`
above returns VayuPress's JSON, and the app connects. The same principle applies
to any CDN or WAF — allow the two stream paths, keep protecting everything else.

### Recommended: a dedicated, proxy-off `talk.<domain>` subdomain (automatic)

If your site sits behind a CDN you can't fully exempt (e.g. a WAF Skip rule isn't
enough against a heavy bot swarm), the robust fix is to serve the VayuTalk relay
on its own subdomain that bypasses the CDN entirely, while the main domain keeps
full protection. VayuPress automates the entire server side — **the only manual
step is one DNS record:**

```
talk.<domain>   A / AAAA   ->  your server's IP    (CDN/proxy OFF — "DNS only")
```

Then run the deploy script (every VayuPress update runs it):

```bash
sudo bash scripts/deploy-vayupress.sh
```

It automatically: adds `talk.<domain>` to the Let's Encrypt certificate, writes a
dedicated nginx vhost that exposes **only** the relay API (SSE never buffered),
and sets `VAYUOS_TALK_HOST`, which makes the server advertise the host in
`/.well-known/vayumail/autoconfig.json`. The VayuMail app reads that, confirms the
subdomain is live, and routes its chat stream there on its own — no app setting to
change. Until the DNS record exists the step is skipped and the app keeps using
the main domain, so nothing breaks in the meantime.

Verify after DNS propagates:

```bash
curl -s https://<domain>/health | grep -o '"version"[^,]*'          # confirm the running build
curl -N -s https://talk.<domain>/api/v1/talk/stream                 # expect JSON invalid-token, NOT a CDN page
```

Because your origin IP is already reachable via `mail.<domain>` (mail must also be
CDN-proxy-off), exposing `talk.<domain>` adds no new exposure. To keep the CDN's
protection meaningful for the main site even though the IP is public, restrict the
main `<domain>` vhost to your CDN's published IP ranges (allow those, deny the
rest); `talk.<domain>` stays open. The relay itself is safe exposed: it is
auth-gated, server-side rate-limited, and size-capped.

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

support@vayupress.com — https://vayupress.com/docs
