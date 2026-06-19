# syntax=docker/dockerfile:1
#
# VayuPress — sovereign single-binary publishing engine.
#
# Multi-stage build: compile the CGO/SQLite binary on a full toolchain, then
# ship a minimal Debian-slim runtime. No telemetry, no third-party services
# baked in — Meilisearch is optional and runs as its own container/service.
#
#   docker build -t vayupress:latest .
#   docker run --rm -p 8080:8080 -e API_KEY=changeme \
#     -v vayupress-data:/var/lib/vayupress vayupress:latest

# ---- build stage ------------------------------------------------------------
FROM golang:1.25-bookworm AS build

# CGO is required for the mattn/go-sqlite3 driver.
ENV CGO_ENABLED=1 GOTOOLCHAIN=auto

WORKDIR /src

# Cache modules first for faster incremental builds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Trimmed, stripped, statically-linked-as-possible binary. main.version is
# stamped from the VERSION build-arg (defaults to "docker").
ARG VERSION=docker
RUN go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/vayupress ./cmd/vayupress

# ---- runtime stage ----------------------------------------------------------
FROM debian:bookworm-slim AS runtime

# ca-certificates: outbound HTTPS for update checks. tzdata: correct timestamps.
# No build tools, no shell utilities beyond the base.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata curl \
    && rm -rf /var/lib/apt/lists/*

# Run as an unprivileged user. The data/cache/media dirs are owned by it so the
# container needs no root at runtime.
RUN useradd --system --uid 10001 --home /var/lib/vayupress vayu \
    && mkdir -p /var/lib/vayupress /var/lib/vayupress/media \
       /var/cache/vayupress /tmp/vayupress /var/www/vayupress/static \
    && chown -R vayu:vayu /var/lib/vayupress /var/cache/vayupress /tmp/vayupress

COPY --from=build /out/vayupress /usr/local/bin/vayupress
# Static assets (admin CSS/JS, fonts) served same-origin under the strict CSP.
COPY --chown=vayu:vayu static/ /var/www/vayupress/static/

ENV DB_PATH=/var/lib/vayupress/data.db \
    CACHE_DIR=/var/cache/vayupress \
    MEDIA_DIR=/var/lib/vayupress/media \
    TMP_DIR=/tmp/vayupress \
    STATIC_DIR=/var/www/vayupress/static \
    PORT=8080 \
    DOMAIN=localhost

USER vayu
WORKDIR /var/lib/vayupress
EXPOSE 8080

# Liveness probe uses the instant /health/live endpoint (no DB writes).
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD curl -fsS http://localhost:8080/health/live || exit 1

ENTRYPOINT ["/usr/local/bin/vayupress"]
