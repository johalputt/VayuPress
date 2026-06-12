# VayuPress
> **Ultra-lightweight, ethical publishing infrastructure.**
> SQLite-first, zero-trust, no tracking. Built to outperform WordPress, Hugo, and Ghost.

## Quick Start
```bash
curl -sSL https://raw.githubusercontent.com/johalputt/vayupress/main/scripts/deploy-vayupress.sh | bash
```

Or clone and deploy manually:
```bash
git clone https://github.com/johalputt/vayupress.git
cd vayupress
sudo ./scripts/deploy-vayupress.sh
```

## What Is VayuPress?

VayuPress ("Vayu" — Sanskrit for wind/speed) is modern publishing infrastructure for developers, writers, and AI-assisted content engines who need:

- **Static-file speed** with dynamic flexibility
- **Single-VPS efficiency** (runs on 12 GB RAM / 6 vCPU / 250 GB NVMe)
- **Total control** over content, hosting, and data
- **No vendor lock-in** — SQLite, Go, Nginx, open standards
- **Zero telemetry** — no tracking, no data harvesting

## Stack

| Component       | Role                                      |
|-----------------|-------------------------------------------|
| Go 1.22         | HTTP server, write-queue workers, renderer|
| SQLite (WAL)    | Primary database (SQLite-first doctrine)  |
| Meilisearch     | Optional full-text search (<50ms p95)     |
| Nginx           | Static-file serving, TLS termination      |
| Isso            | Self-hosted, privacy-friendly comments    |

## Requirements

- Ubuntu 24.04 LTS
- 8 GB RAM minimum (12 GB recommended)
- 4 vCPU minimum (6 vCPU recommended)
- 50 GB NVMe minimum (250 GB for 1M+ posts with media)
- Root or sudo access

## Documentation

| Document                         | Description                          |
|----------------------------------|--------------------------------------|
| [INSTALLATION](docs/INSTALLATION.md)   | Full installation guide        |
| [ARCHITECTURE](docs/ARCHITECTURE.md)   | System design and data flow    |
| [API-REFERENCE](docs/API-REFERENCE.md) | REST API reference             |
| [DEVELOPMENT](docs/DEVELOPMENT.md)     | Local development setup        |
| [TROUBLESHOOTING](docs/TROUBLESHOOTING.md) | Common issues & fixes      |
| [SECURITY](SECURITY.md)                | Vulnerability disclosure       |
| [CONTRIBUTING](CONTRIBUTING.md)        | How to contribute              |
| [GOVERNANCE](GOVERNANCE.md)            | Governance overview            |
| [ETHICS](ETHICS.md)                    | Ethical principles & AI charter|
| [CHANGELOG](CHANGELOG.md)              | Version history                |

## Governance

VayuPress is governed by the [VayuPress Governance Constitution v6.0](GOVERNANCE-CONSTITUTION.md).

**Priority order**: Security = Data Integrity > Ethical Compliance > Reliability > Simplicity > Performance > DX > Feature Velocity

## Contact

| Purpose              | Email                          |
|----------------------|--------------------------------|
| General              | hello@vayupress.com            |
| Support              | support@vayupress.com          |
| Security             | security@vayupress.com         |
| Ethics violations    | ethics@vayupress.com           |
| Governance / RFCs    | governance@vayupress.com       |

## License

MIT — see [LICENSE](LICENSE).

> *"Stay lightweight. Stay fast. Stay secure. Stay disciplined. Stay ethical."*
