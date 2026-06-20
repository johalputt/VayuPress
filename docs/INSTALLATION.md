# VayuPress Installation Guide

## Requirements

| Resource | Minimum     | Recommended |
|----------|-------------|-------------|
| OS       | Ubuntu 24.04 LTS | Ubuntu 24.04 LTS |
| RAM      | 8 GB        | 12 GB       |
| CPU      | 4 vCPU      | 6 vCPU      |
| Disk     | 50 GB NVMe  | 250 GB NVMe |
| Access   | Root / sudo | Root / sudo |

## Quick Install (Recommended)

```bash
curl -sSL https://raw.githubusercontent.com/johalputt/vayupress/main/scripts/deploy-vayupress.sh | bash
```

## Manual Install

### 1. Clone the repository

```bash
git clone https://github.com/johalputt/vayupress.git
cd vayupress
```

### 2. Configure

Edit `scripts/deploy-vayupress.sh` and set:

```bash
DOMAIN="yourdomain.com"
EMAIL="admin@yourdomain.com"
WORKER_COUNT=3
STORAGE_QUOTA_GB=200
```

### 3. Run the deploy script

```bash
sudo ./scripts/deploy-vayupress.sh
```

Options:
```bash
sudo ./scripts/deploy-vayupress.sh --dry-run  # Validate only, no changes
sudo ./scripts/deploy-vayupress.sh --upgrade  # Upgrade existing installation
```

### 4. Verify

After deploy, check:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/health/ready
```

Expected: `{"status":"ok"}` from both endpoints.

## What the Deploy Script Installs

1. System dependencies (curl, wget, git, nginx, sqlite3, certbot, fail2ban, ufw)
2. Go 1.22.5
3. Meilisearch (optional search subsystem)
4. Isso (self-hosted comments)
5. Self-hosted fonts (Inter, IBM Plex Mono)
6. VayuPress Go application
7. Systemd services (vayupress, meilisearch, isso)
8. Nginx config with TLS (Let's Encrypt via Certbot)
9. UFW firewall (ports 22, 80, 443)
10. Logrotate, cron jobs (backup, orphan cleanup, restore validation)

## Directory Layout

```
/var/www/vayupress/src/      # Go source
/var/www/vayupress/static/   # Static assets (CSS, fonts, media)
/var/cache/vayupress/        # Rendered HTML cache
/var/lib/vayupress/          # SQLite database
/var/log/vayupress/          # Application logs
/tmp/vayupress/              # Ephemeral upload temp (noexec, auto-cleaned)
/backups/                    # SQLite backups
```

## Environment Variables

| Variable              | Default                        | Description                        |
|-----------------------|--------------------------------|------------------------------------|
| `API_KEY`             | (required)                     | Admin API key                      |
| `DB_PATH`             | `/var/lib/vayupress/data.db`   | SQLite database path               |
| `CACHE_DIR`           | `/var/cache/vayupress`         | Rendered HTML cache directory      |
| `MEDIA_DIR`           | `/var/lib/vayupress/media`     | Editor image uploads (served at `/media/`) |
| `MEILI_HOST`          | `http://localhost:7700`        | Meilisearch URL                    |
| `MEILI_MASTER_KEY`    | (generated)                    | Meilisearch master key             |
| `DOMAIN`              | `localhost`                    | Public domain                      |
| `PORT`                | `8080`                         | HTTP listen port                   |
| `WORKER_COUNT`        | `3`                            | Write queue workers                |
| `STORAGE_QUOTA_GB`    | `200`                          | Max storage quota (GB)             |
| `MEDIA_RETAIN_DAYS`   | `365`                          | Days to retain media               |
| `BACKUP_RETAIN_DAYS`  | `30`                           | Days to retain backups             |
| `MAX_REPLAY_COUNT`    | `3`                            | Max dead-letter replay attempts    |
| `WAL_SIZE_THRESHOLD_MB`| `32`                          | WAL size to trigger RESTART checkpoint|
| `VAYU_MAINTENANCE`    | `false`                        | Enable maintenance mode            |
| `VAYU_SELFUPDATE_ENABLED`| `false`                     | Opt-in for `vayupress update apply` (see UPGRADING.md) |
| `VAYU_RELEASE_PUBKEY` | (unset)                        | Hex Ed25519 key the signed apply verifies against |
| `SMTP_HOST`           | (unset)                        | SMTP server for email delivery. Empty = email disabled (no-op) |
| `SMTP_PORT`           | `587`                          | SMTP submission port               |
| `SMTP_USERNAME`       | (unset)                        | SMTP auth username (omit for unauthenticated relays) |
| `SMTP_PASSWORD`       | (unset)                        | SMTP auth password                 |
| `SMTP_FROM`           | `VayuPress <noreply@$DOMAIN>`  | From header / envelope sender      |
| `SMTP_TLS`            | `starttls`                     | `starttls` (587), `ssl` (465), or `none` (trusted localhost) |
| `SCHEDULER_TICK_SEC`  | `60`                           | Scheduled-publish poll interval (seconds); `0` disables |
| `ANALYTICS_RETAIN_DAYS`| `365`                         | Retention window for privacy-first view aggregates |
| `SOCIAL_MASTODON_INSTANCE`| (unset)                    | Mastodon-compatible base URL for auto-posting (e.g. `https://mastodon.social`) |
| `SOCIAL_MASTODON_TOKEN`| (unset)                       | Mastodon app access token (`write:statuses` scope) |
| `VAYU_AI_URL`         | (unset)                        | Local Ollama base URL for the AI writing assistant (e.g. `http://localhost:11434`) |
| `VAYU_AI_MODEL`       | `llama3.2`                     | Ollama model name for the assistant |

### Email delivery (Tier 1)

VayuPress sends email over plain SMTP using only the Go standard library — no
third-party SDKs, no hosted APIs, no telemetry. Set `SMTP_HOST` (plus
credentials) to enable:

- **Double opt-in confirmations** are emailed automatically on newsletter
  subscribe.
- **Broadcasts** to all confirmed subscribers via
  `POST /api/v1/admin/newsletter/broadcast` (`{subject, text, html}`), each with
  an auto-appended unsubscribe link.

When `SMTP_HOST` is empty, every email call is a safe no-op: subscriber and
comment flows keep working, delivery is simply skipped and audit-logged.

### AI writing assistant (Tier 2)

An opt-in, **sovereign** writing assistant that talks to a LOCAL,
operator-run [Ollama](https://ollama.com) server — nothing is ever sent to a
hosted third-party model. Set `VAYU_AI_URL` (and optionally `VAYU_AI_MODEL`) to
enable. Operations via `POST /api/v1/admin/ai/assist` (`{op, text}`):
`summarize`, `improve`, `titles`, `seo`, `continue`. Probe availability at
`GET /api/v1/admin/ai/status`.

The assistant only *suggests* — it never auto-edits content, consistent with the
project's "no autonomous actions" ethics charter.

### Social auto-posting (Tier 2)

When `SOCIAL_MASTODON_INSTANCE` + `SOCIAL_MASTODON_TOKEN` are set, each newly
published article is automatically shared to your Mastodon-compatible server
(Mastodon, Pleroma, Akkoma). A single app access token is all that's needed — no
OAuth redirect flow. Sharing is async and best-effort; a failed toot never
affects publishing, and an idempotency key prevents duplicate posts on retry.

### Migrating from Ghost & WordPress (Tier 2)

Two built-in importers move content off the most common platforms with no
external tooling:

```bash
# Ghost — Settings → Export → download the JSON
vayupress migrate ghost --file ghost-export.json --dry-run   # preview
vayupress migrate ghost --file ghost-export.json             # import

# WordPress — Tools → Export → All content (WXR/XML)
vayupress migrate wordpress --file wordpress-export.xml --dry-run
vayupress migrate wordpress --file wordpress-export.xml
```

Both preserve titles, slugs, publish dates, tags/categories, and draft status
(`--skip-drafts` is on by default), sanitise HTML through the same policy as the
write queue, and dedupe by slug so re-running is safe.

### Privacy-first analytics (Tier 2)

Cookieless, consent-free page-view counting that stores **no IP addresses, no
user agents, no cookies, no fingerprints, and no per-visitor rows** — only daily
aggregate counts per path and per referrer host. There is nothing in the schema
that can identify a reader. Read the rollup at
`GET /api/v1/admin/analytics?days=30&limit=20`. Aggregates older than
`ANALYTICS_RETAIN_DAYS` are pruned daily.

### Outbound webhooks (Tier 2)

Register endpoints that receive a signed JSON POST when content changes —
integrate with Zapier, n8n, Make, or any custom service. Each delivery carries
`X-VayuPress-Signature: sha256=<hmac>` over the raw body (per-hook secret) plus
`X-VayuPress-Event`. Subscribe to `article.created.v1`, `article.updated.v1`,
`article.deleted.v1`, or `*`. Bounded retry/backoff; every attempt is recorded.

```bash
# Register a webhook (secret returned once, on creation)
curl -X POST https://$DOMAIN/api/v1/admin/webhooks \
  -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/hook","events":["article.created.v1","article.updated.v1"]}'
```

Manage with `GET /api/v1/admin/webhooks`,
`DELETE /api/v1/admin/webhooks/{id}`, and inspect delivery history at
`GET /api/v1/admin/webhooks/{id}/deliveries`.

### Multi-author accounts & password login (Tier 1)

VayuPress supports per-author accounts with email + password sign-in, in
addition to the legacy single API key. Passwords are hashed with Argon2id;
login sessions are stored server-side in SQLite (only the SHA-256 of each token
is persisted) and carried in a hardened `HttpOnly`, `SameSite=Lax` cookie.

Bootstrap the first admin from the CLI (the only path that needs no existing
session):

```bash
vayupress user add alice@example.com 'a-strong-password' Alice --admin
vayupress user list
vayupress user passwd alice@example.com 'new-password'
vayupress user delete bob@example.com
```

Sign in at `/admin/v2/login`. Admin-role users can also manage accounts over the
API: `GET/POST /api/v1/admin/users`, `DELETE /api/v1/admin/users/{email}`. The
existing API-key path keeps working unchanged — admin pages accept **either** a
valid API key **or** a login session.

### Image optimization (Tier 1)

Editor image uploads are automatically optimized using a **stdlib-only** pipeline
(no libvips, no CGO, no third-party scaling libraries). PNG and JPEG uploads
wider than 1600px are downscaled proportionally with area-averaging resampling
and re-encoded; the smaller of the optimized/original bytes always wins.
Animated GIF and WebP pass through untouched to preserve animation and format.
The upload response now includes `width` and `height`.

### Scheduled publishing (Tier 1)

Stage future-dated posts with `POST /api/v1/admin/schedule`
(`{slug, title, content, tags[], publish_at}` where `publish_at` is RFC3339). A
durable SQLite-backed ticker promotes each post through the normal
render → index → cache pipeline when its time arrives. Posts staged while the
server was down are caught up on the next startup tick. List with
`GET /api/v1/admin/schedule`; cancel with `DELETE /api/v1/admin/schedule/{id}`.

## Docker

A multi-stage `Dockerfile` and `docker-compose.yml` ship in the repo root for a
container deployment. The image compiles the CGO/SQLite binary, then runs it as
an unprivileged user on a minimal Debian-slim base with a built-in healthcheck.

```bash
cp .env.example .env
# edit .env: set a strong API_KEY (openssl rand -hex 32) and your DOMAIN
docker compose up -d --build
```

VayuPress listens on plain HTTP `:8080` (bound to loopback in the compose file)
and expects a **TLS-terminating reverse proxy** in front that sets
`X-Forwarded-For`. A minimal nginx server block:

```nginx
server {
    listen 443 ssl http2;
    server_name example.com;

    ssl_certificate     /etc/letsencrypt/live/example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/example.com/privkey.pem;

    client_max_body_size 12m;   # headroom for 8 MB editor image uploads

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
    }
}
```

Persistent state lives in two named volumes: `vayupress-data`
(`/var/lib/vayupress` — SQLite DB **and** uploaded media) and `vayupress-cache`
(`/var/cache/vayupress` — rendered HTML, sitemap, feed). Back up the former; the
latter is regenerable.

### Backup (Docker)

```bash
# Hot backup of the SQLite DB + media to a tarball on the host:
docker run --rm -v vayupress-data:/data -v "$PWD":/backup debian:bookworm-slim \
  tar czf /backup/vayupress-$(date +%F).tar.gz -C /data .
```

For online, WAL-safe backups and restore, the bundled `vayu-backup` tool and
[docs/operations/backup-restore.md](operations/backup-restore.md) remain the
recommended path.

## Upgrade

```bash
cd vayupress
git pull origin main
sudo ./scripts/deploy-vayupress.sh --upgrade
```

The `--upgrade` flag preserves existing secrets and data. For container
deployments, rebuild and recreate: `docker compose up -d --build`. See
[docs/UPGRADING.md](UPGRADING.md) for the signed self-update path.

## Uninstall

```bash
sudo systemctl stop vayupress meilisearch isso
sudo systemctl disable vayupress meilisearch isso
sudo rm -f /etc/systemd/system/vayupress.service
sudo rm -f /etc/systemd/system/meilisearch.service
sudo rm -f /etc/systemd/system/isso.service
# Optionally remove data:
# sudo rm -rf /var/lib/vayupress /var/cache/vayupress /var/log/vayupress
```

## Support

support@vayupress.com — https://docs.vayupress.com
