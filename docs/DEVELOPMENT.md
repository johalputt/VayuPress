# VayuPress Development Guide

## Goal

A new developer should be able to run `make dev` in under 10 minutes and make their first change.

## Prerequisites

- Go 1.22+
- SQLite3 (`apt install sqlite3`)
- (Optional) Meilisearch for full-text search testing

## Quick Start

```bash
git clone https://github.com/johalputt/vayupress.git
cd vayupress

# Set required environment variables
export API_KEY=dev-secret-key
export DB_PATH=/tmp/vayupress-dev.db
export CACHE_DIR=/tmp/vayupress-cache
export DOMAIN=localhost
export PORT=8080

# Create cache directory
mkdir -p /tmp/vayupress-cache

# Run the application
cd /var/www/vayupress/src  # or wherever main.go lives
go run main.go
```

The server starts at `http://localhost:8080`.

## Development Workflow

```bash
# Run tests
go test ./... -v

# Run tests with race detector (mandatory before PR)
go test -race ./...

# Run linter
golangci-lint run

# Run vulnerability scan
govulncheck ./...

# Build binary
go build -o vayupress main.go

# Check binary size (must be <45 MB compressed)
gzip -c vayupress | wc -c
```

## Environment Variables for Development

```bash
export API_KEY=dev-secret-key
export DB_PATH=/tmp/vayupress-dev.db
export CACHE_DIR=/tmp/vayupress-cache
export DOMAIN=localhost
export PORT=8080
export MEILI_HOST=http://localhost:7700   # optional
export VAYU_MAINTENANCE=false
export VAYU_MIGRATE_DRY_RUN=false        # set true to preview migrations
```

## Making a Change

1. Create a branch: `git checkout -b feat/my-change`
2. Make your change.
3. Add/update tests.
4. Run `go test -race ./...` — must pass.
5. Run `golangci-lint run` — must pass.
6. Update `CHANGELOG.md`.
7. Update docs if you changed behavior.
8. Sign your commit: `git commit -s -m "feat: description"`
9. Open a PR.

## Architecture Overview

See [ARCHITECTURE.md](ARCHITECTURE.md) for diagrams and component descriptions.

Key constraints for development:
- **SQLite-first**: every feature must work without Meilisearch.
- **No heavy frontend**: public paths use HTMX + Alpine.js only.
- **No new dependencies** without RFC approval.
- **No breaking API changes** without a MAJOR version bump.

## Test Requirements

| Test Type       | Command                          | Threshold            |
|-----------------|----------------------------------|----------------------|
| Unit tests      | `go test ./...`                  | Coverage ≥ 70%       |
| Race detector   | `go test -race ./...`            | Zero races           |
| Integration     | `go test -tags integration ./...`| Must exist           |
| Linting         | `golangci-lint run`              | Zero errors          |
| Vulnerability   | `govulncheck ./...`              | No High/Critical CVEs|

## Debugging

```bash
# Inspect write queue
curl -H "Authorization: Bearer $API_KEY" http://localhost:8080/admin/queue

# Check health
curl http://localhost:8080/health/ready

# Prometheus metrics
curl http://localhost:8080/metrics

# pprof (localhost only, rate-limited)
curl http://localhost:6060/debug/pprof/
```

## Common Issues

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md).

## Contact

community@vayupress.com — governance@vayupress.com
