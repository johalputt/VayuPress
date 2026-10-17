# VayuPress Development Guide

## Goal

A new developer should be able to run VayuPress locally in under ten minutes and
make their first change.

## Prerequisites

- **Go** — the version in `go.mod`'s `toolchain` line (go1.27.2 today). With
  `GOTOOLCHAIN=auto`, an older Go downloads it for you.
- **A C compiler** (`build-essential`): SQLite is linked through CGO.
- **Chromium and Node**, only for the browser tests (`tests/e2e`).

## Run it

```bash
git clone https://github.com/johalputt/vayupress.git
cd vayupress

export API_KEY=dev-secret-key            # required; the server exits without it
export DOMAIN=localhost
export PORT=8080
export DB_PATH=/tmp/vp/vayupress.db
export CACHE_DIR=/tmp/vp/cache
export MEDIA_DIR=/tmp/vp/media
export TMP_DIR=/tmp/vp/tmp
export STATIC_DIR="$PWD/static"          # the binary writes its assets here at boot
export VAYU_DOCS_DIR="$PWD/docs"
mkdir -p /tmp/vp/cache /tmp/vp/media /tmp/vp/tmp

make dev            # or: go run ./cmd/vayupress
```

The site is at `http://localhost:8080` and the console at
`http://localhost:8080/os`. A fresh database creates `admin@localhost` with a
random password, written to a file beside the database and logged once; the
console asks for a new one at first sign-in.

Seed a post through the API:

```bash
curl -X POST http://localhost:8080/api/v1/articles -H "X-API-Key: $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"title":"Hello","slug":"hello","content":"<p>First post.</p>","tags":["welcome"]}'
```

VayuShield challenges a client that does not look like a browser; test pages
with a browser, or with the API key.

## The source

One Go module: the server is `cmd/vayupress` (its handlers, the console's pages
in `admin_os_*.go`, routes in `routes.go`), and everything else is under
`internal/`, one package per concern — the map is in
[ARCHITECTURE.md](ARCHITECTURE.md). The tools under `tools/` (importers,
`vayu-compat`, `vayu-backup`) are separate modules.

Static assets (`static/`) and the docs are compiled into the binary and written
to `STATIC_DIR` at boot, so a release replaces them with the binary.

## Checks to run before you commit

```bash
gofmt -l .                          # must print nothing
go vet ./...
go build ./...
go test ./...                       # unit tests
go test -race ./...
go test -tags integration ./...     # integration
staticcheck ./...
golangci-lint run                   # zero issues
gosec -severity high -confidence high ./...
govulncheck ./...
make check-size                     # binary under 45 MB gzipped
```

Browser tests, against a running install:

```bash
cd tests/e2e && npm ci && npx playwright test
```

[QUALITY-GATES.md](QUALITY-GATES.md) lists every gate CI runs and what each
does not prove.

## Making a change

1. Branch: `git checkout -b feat/my-change`.
2. Make the change, with a test that fails without it — run the test once
   against the code with the change undone and watch it fail at its own
   assertion.
3. Run the checks above.
4. Add a line under `## [Unreleased]` in `CHANGELOG.md`, in the reader's words.
5. Update the docs the change makes untrue.
6. Sign off: `git commit -s` ([Developer Certificate of Origin](https://developercertificate.org/)).
7. Open a pull request.

Key constraints:
- **SQLite only**: every feature works with the one database — no external
  service.
- **No framework, no build step**: server-rendered Go; htmx and Alpine's CSP
  build where a page needs behaviour.
- **No new dependency** without a reason recorded in an ADR.
- **No breaking API change** without a major version
  ([compatibility/api-contracts.md](compatibility/api-contracts.md)).

## Debugging

```bash
curl http://localhost:8080/health/dependencies
curl -H "X-API-Key: $API_KEY" http://localhost:8080/api/v1/queue
curl http://localhost:8080/metrics
curl -H "X-API-Key: $API_KEY" "http://localhost:8080/debug/pprof/goroutine?debug=1"
```

Detail health, `/metrics` and pprof answer requests from the machine itself or
carrying the API key.

## Common Issues

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md).

## Contact

community@vayupress.com — governance@vayupress.com
