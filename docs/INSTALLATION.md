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
