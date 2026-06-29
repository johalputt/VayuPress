# VayuPress Quick Setup

The fastest way to get VayuPress running on a fresh Ubuntu 24.04 server.

## Prerequisites

| Requirement | Minimum       | Recommended   |
|-------------|---------------|---------------|
| OS          | Ubuntu 24.04  | Ubuntu 24.04  |
| RAM         | 8 GB          | 12 GB         |
| CPU         | 4 vCPU        | 6 vCPU        |
| Disk        | 50 GB NVMe    | 250 GB NVMe   |
| Access      | root / sudo   | root / sudo   |

## 1-Command Install

```bash
curl -sSL https://raw.githubusercontent.com/johalputt/vayupress/main/scripts/deploy-vayupress.sh | bash
```

> Tip: Run with `--dry-run` first to preview all actions without making changes:
> ```bash
> sudo ./scripts/deploy-vayupress.sh --dry-run
> ```

## What Gets Installed

| Component     | Purpose                              |
|---------------|--------------------------------------|
| Go 1.22       | Application runtime                  |
| Nginx         | Static file serving + TLS            |
| SQLite        | Primary database (WAL mode)          |
| VayuFind      | Built-in instant full-text search    |
| Isso          | Self-hosted comment server           |
| Certbot       | Automatic Let's Encrypt TLS          |
| fail2ban      | Brute-force protection               |
| UFW           | Firewall (ports 22, 80, 443)         |

## Configuration

Before running, edit these variables in `scripts/deploy-vayupress.sh`:

```bash
DOMAIN="yourdomain.com"         # Your domain (or leave as vayupress.com for local)
EMAIL="admin@yourdomain.com"    # Let's Encrypt certificate email
WORKER_COUNT=3                  # Write queue workers (increase for high traffic)
STORAGE_QUOTA_GB=200            # Max storage in GB
```

## Verify Installation

After deploy completes, run these health checks:

```bash
# Liveness
curl http://localhost:8080/health

# Readiness (DB, search, storage)
curl http://localhost:8080/health/ready

# All subsystems
curl http://localhost:8080/health/dependencies
```

All should return `{"status":"ok"}`.

## Upgrade Existing Installation

```bash
git pull origin main
sudo ./scripts/deploy-vayupress.sh --upgrade
```

The `--upgrade` flag loads your existing secrets and preserves all data.

## Next Steps

- **Full installation guide**: [docs/INSTALLATION.md](INSTALLATION.md)
- **API reference**: [docs/API-REFERENCE.md](API-REFERENCE.md)
- **Architecture overview**: [docs/ARCHITECTURE.md](ARCHITECTURE.md)
- **Troubleshooting**: [docs/TROUBLESHOOTING.md](TROUBLESHOOTING.md)

## Support

support@vayupress.com
