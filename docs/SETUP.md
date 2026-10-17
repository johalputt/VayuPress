# VayuPress Quick Setup

The fastest way to get VayuPress running on a fresh Ubuntu 24.04 server.

## Prerequisites

| Requirement | Minimum | Runs johal.in (234,727 posts, 14 sites) |
|-------------|---------|----------------------------------------|
| OS | Ubuntu 24.04 | Ubuntu 24.04 |
| RAM | 8 GB | a Contabo VPS 10 |
| CPU | 4 vCPU | |
| Disk | 50 GB NVMe | |
| Access | root / sudo | |

Point the domain's apex and `www` at the server first; the DNS records for mail,
chat, MCP and the API are in the [README](../README.md#quick-start).

## 1-Command Install

```bash
curl -sSL https://raw.githubusercontent.com/johalputt/vayupress/main/scripts/deploy-vayupress.sh | sudo DOMAIN=yourdomain.com EMAIL=you@yourdomain.com bash
```

To see what it would do without changing anything:

```bash
sudo DOMAIN=yourdomain.com ./scripts/deploy-vayupress.sh --dry-run
```

## What Gets Installed

| Component | Purpose |
|-----------|---------|
| Go (the version `go.mod` names, when the host's is older) | Builds the binary |
| VayuPress | The binary, linked at `/usr/local/bin/vayupress`, run by systemd as `www-data` |
| Nginx | TLS and the reverse proxy |
| Certbot | Let's Encrypt certificates, renewed by its timer |
| fail2ban | Bans on nginx's auth and rate-limit errors |
| UFW | Firewall: 22, 80, 443 and the mail ports |
| logrotate | Log rotation |
| Tor, obfs4proxy | Onion services, dormant until VayuTor is switched on |

Search (VayuFind), the mail server, chat, analytics and the bot shield are part
of the binary; nothing else is installed for them.

## Configuration

Pass these as environment variables to the installer; it writes them, with
generated secrets, to `/etc/vayupress/env`:

```bash
DOMAIN=yourdomain.com          # your domain
EMAIL=admin@yourdomain.com     # the Let's Encrypt account email
WORKER_COUNT=3                 # write-queue workers
```

## Verify Installation

```bash
curl http://localhost:8080/health                 # liveness and version
curl http://localhost:8080/health/ready           # database and workers
curl http://localhost:8080/health/dependencies    # every part
```

The installer creates `admin@yourdomain.com` with a random password, saved to a
root-only file and printed at the end; sign in at `https://yourdomain.com/os`
and set your own.

## Upgrade Existing Installation

Updates install from VayuOS → System › Update, verified against the release's
signature. From the shell:

```bash
cd /opt/vayupress && git pull origin main
sudo ./scripts/deploy-vayupress.sh --upgrade
```

`--upgrade` keeps your secrets and data.

## Next Steps

- **Full installation guide**: [docs/INSTALLATION.md](INSTALLATION.md)
- **API reference**: [docs/API-REFERENCE.md](API-REFERENCE.md)
- **Architecture overview**: [docs/ARCHITECTURE.md](ARCHITECTURE.md)
- **Troubleshooting**: [docs/TROUBLESHOOTING.md](TROUBLESHOOTING.md)

## Support

support@vayupress.com
