# VayuPress

[![CI](https://github.com/johalputt/vayupress/actions/workflows/ci.yml/badge.svg)](https://github.com/johalputt/vayupress/actions/workflows/ci.yml)
[![Security](https://github.com/johalputt/vayupress/actions/workflows/security.yml/badge.svg)](https://github.com/johalputt/vayupress/actions/workflows/security.yml)
[![Go](https://img.shields.io/badge/go-1.25-blue)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![Constitution](https://img.shields.io/badge/constitution-v6.0%20P1--P12-blueviolet)](GOVERNANCE-CONSTITUTION.md)

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
| Go 1.25         | HTTP server, write-queue workers, renderer|
| SQLite (WAL)    | Primary database (SQLite-first doctrine)  |
| Meilisearch     | Optional full-text search (<50ms p95)     |
| Nginx           | Static-file serving, TLS termination      |
| Isso            | Self-hosted, privacy-friendly comments    |

## Architecture

```
                     ┌─────────────────────────────────┐
                     │           Internet               │
                     └────────────────┬────────────────┘
                                      │ HTTPS (443)
                     ┌────────────────▼────────────────┐
                     │    Nginx (TLS termination,       │
                     │    static files, gzip, CSP)      │
                     └────────────────┬────────────────┘
                                      │ HTTP (127.0.0.1:8080)
             ┌────────────────────────▼────────────────────────┐
             │              VayuPress Go Binary                 │
             │  ┌─────────┐  ┌──────────┐  ┌───────────────┐  │
             │  │  Router │  │  Worker  │  │  Write Queue  │  │
             │  │  (chi)  │  │  Pool    │  │  (async)      │  │
             │  └────┬────┘  └────┬─────┘  └───────┬───────┘  │
             │       │            │                 │           │
             │  ┌────▼────────────▼─────────────────▼───────┐  │
             │  │           SQLite (WAL mode)                │  │
             │  │   articles · media · audit_log (WORM)     │  │
             │  └────────────────────────────────────────────┘  │
             └──────────────────────────┬──────────────────────┘
                                        │
              ┌─────────────────────────┼──────────────────────┐
              │                         │                       │
   ┌──────────▼──────────┐  ┌──────────▼──────────┐  ┌────────▼───────┐
   │  Meilisearch        │  │  Isso               │  │  fail2ban /    │
   │  (optional search)  │  │  (self-hosted       │  │  UFW firewall  │
   │  <50ms p95 search   │  │   comments)         │  │                │
   └─────────────────────┘  └─────────────────────┘  └────────────────┘
```

**Key design decisions:**
- Single Go binary — no runtime dependencies beyond SQLite
- SQLite WAL mode — concurrent reads, serialized writes, no connection pooling overhead
- SSRF-safe outbound client — blocks loopback, link-local (169.254.169.254), RFC-1918
- Immutable WORM audit log — SQLite ABORT triggers prevent modification
- Self-hosted fonts (Inter + IBM Plex Mono) — zero external requests, ADR-0002

## Performance

Target: ≤50ms p95 latency on a 4-vCPU / 8 GB VPS under sustained load.

| Metric | Target | Architecture decision |
|--------|--------|-----------------------|
| Article page p95 | <50ms | Nginx static-file serving + in-memory cache |
| Search p95 | <50ms | Meilisearch with pre-warmed index |
| API write p95 | <100ms | SQLite WAL + async write queue |
| Cold start | <500ms | Single static binary, no JVM/interpreter |
| Binary size (gzip) | <45 MB | `-ldflags="-s -w" -trimpath` |

Run benchmarks locally: `make bench`

## Repository Structure

```
vayupress/
├── cmd/vayupress/main.go      # Real Go source (mirrors the deploy heredoc, P13)
├── go.mod / go.sum            # Pinned dependencies (Go 1.25)
├── scripts/
│   ├── deploy-vayupress.sh    # Canonical self-contained installer (curl | bash)
│   └── sync-source.sh         # Keeps cmd/vayupress/main.go == deploy heredoc
├── docs/                      # Architecture, operations, ADRs, threat model
│   ├── adr/                   # Architecture Decision Records (ADR-0001 … 0044)
│   └── operations/            # Disaster-recovery and ops runbooks
├── .github/workflows/         # CI (governance + native Go) and security
├── Makefile                   # build / test / lint / sync / governance targets
└── GOVERNANCE-CONSTITUTION.md # The 13 Prompts
```

**Source-of-truth model (P13):** the deploy script's embedded heredoc is *canonical*
so `curl | bash` keeps working. `cmd/vayupress/main.go` is an exact, gofmt-clean
mirror that enables native `go build`, `go vet`, `go test`, `golangci-lint`, and
`govulncheck`. CI fails the build if the two ever drift (`scripts/sync-source.sh
--check`). See [ADR-0044](docs/adr/ADR-0044-repository-decomposition.md).

```bash
# Build and test the real Go tree natively
go build ./...
go vet ./...
make sync-check     # verify the mirror matches the deploy script
```

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
| [UPGRADING](UPGRADING.md)              | Upgrade procedures             |
| [OPERATIONS](docs/OPERATIONS.md)       | Runbooks and incident response |
| [THREAT MODEL](docs/THREAT-MODEL.md)   | Security threat analysis       |

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
