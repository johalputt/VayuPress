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

## Upgrade

```bash
cd vayupress
git pull origin main
sudo ./scripts/deploy-vayupress.sh --upgrade
```

The `--upgrade` flag preserves existing secrets and data.

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
