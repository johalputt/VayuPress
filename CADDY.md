# Caddy reverse proxy

Caddy terminates TLS in front of VayuPress. The default configuration is
development-friendly and proxies every request. Production has an opt-in IP
allowlist that adds a network boundary around the browser administration
routes; it does not replace VayuPress authentication or CSRF protection.

Both configurations use stock Caddy security controls: 10-second request-header
and 5-minute request-body read deadlines, a 2-minute idle timeout, 32 KB request
and upstream-response header limits, a 50 MB request-body ceiling, bounded
upstream connection/response/write waits, and a 24-hour ceiling for streaming
connections. HTTP/3 0-RTT is disabled to avoid early-data replay of
state-changing requests. WebSocket upgrades and immediate Server-Sent Event
flushing are handled automatically by Caddy.

Caddy writes structured JSON access logs to container stdout; view or export
them with `docker compose logs caddy`. Sensitive `Cookie`, `Set-Cookie`,
`Authorization`, and `Proxy-Authorization` header values are redacted by Caddy.

Defense-in-depth security headers are added only when VayuPress did not already
set them. VayuPress remains authoritative, especially for its per-request
nonce-based Content Security Policy; the Caddyfiles deliberately do not define
a static CSP.

## Development

```bash
cp .env.example .env
# Set API_KEY.
docker compose up -d --build
```

The default `DOMAIN=localhost` uses Caddy's internal CA. Browsers warn until its
root certificate is trusted. Test without installing it with:

```bash
curl -k https://localhost/health/ready
```

To trust the local CA, copy its root certificate and import it into the
operating system or browser trust store:

```bash
docker compose cp caddy:/data/caddy/pki/authorities/local/root.crt ./caddy-root.crt
```

## Production with an administration allowlist

Point the domain's A/AAAA records at the host and allow inbound TCP ports 80 and
443. Caddy provisions and renews the public TLS certificate automatically.

```bash
cp .env.production.example .env.production
# Set DOMAIN, a strong API_KEY, and every operator IP/network.
docker compose --env-file .env.production \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  up -d --build
```

`ADMIN_ALLOWED_IPS` is required and is a space-separated list of IPv4 or IPv6
addresses and CIDR ranges:

```dotenv
ADMIN_ALLOWED_IPS=203.0.113.25 198.51.100.0/24 2001:db8:1234::/48
```

If it is missing or empty, Compose refuses to create the production deployment.
The production Caddyfile protects the exact and nested routes `/os`, `/os/*`,
`/admin`, and `/admin/*`. An allowed client is proxied normally. Every other
client receives a plain `404 Not Found`, and its request never reaches
VayuPress. All other paths remain public.

`/api/v1/admin` remains reachable because authenticated external automation may
depend on it. To protect it too, add `/api/v1/admin` and `/api/v1/admin/*` to
both `path` matchers in `Caddyfile.production`.

### Update or validate the allowlist

Edit `ADMIN_ALLOWED_IPS` in `.env.production`, then validate the rendered
configuration and Caddyfile before recreating Caddy:

```bash
docker compose --env-file .env.production \
  -f docker-compose.yml -f docker-compose.production.yml config
docker compose --env-file .env.production \
  -f docker-compose.yml -f docker-compose.production.yml \
  run --rm --no-deps caddy caddy validate --config /etc/caddy/Caddyfile
docker compose --env-file .env.production \
  -f docker-compose.yml -f docker-compose.production.yml \
  up -d --no-deps --force-recreate caddy
```

To reload an edited mounted Caddyfile without recreating the container:

```bash
docker compose --env-file .env.production \
  -f docker-compose.yml -f docker-compose.production.yml \
  exec caddy caddy reload --config /etc/caddy/Caddyfile
```

Keep an existing shell open while testing a new allowlist. If locked out, use
host console or SSH access to correct `.env.production` and force-recreate
Caddy with the command above. The allowlist affects HTTP administration routes,
not host-level SSH.

### Proxies in front of Caddy

The production matcher uses Caddy's `remote_ip`, which is safe when clients
connect directly to Caddy. Behind a CDN or load balancer it sees the proxy
address instead. Such deployments must configure explicit `trusted_proxies`
and change the matcher to `client_ip`; never trust forwarded client-IP headers
from arbitrary peers.

### Stock Caddy boundary

Stock Caddy does not include Nginx-style per-IP request or concurrent-connection
rate limiting. VayuPress's authentication, endpoint limits, CSRF protection,
and in-process VayuShield controls therefore remain essential. Host firewall
rules can provide an additional boundary without replacing the stock image.

## Operations

```bash
docker compose ps
docker compose logs -f caddy
docker compose exec caddy caddy validate --config /etc/caddy/Caddyfile
docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile
```

Caddy starts and becomes healthy before Compose starts VayuPress. Its
certificates and runtime configuration persist in `caddy-data` and
`caddy-config`. Do not delete `caddy-data` during routine upgrades because it
contains certificate private keys and other TLS state.
