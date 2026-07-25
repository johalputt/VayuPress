# Caddy reverse proxy

The default Docker Compose deployment provisions Caddy in front of VayuPress.
Caddy starts and becomes healthy first; only then does Compose start the
VayuPress container.

## Start

```bash
cp .env.example .env
# Set API_KEY. For production, also set DOMAIN.
docker compose up -d --build
```

For a public deployment, point the domain's A/AAAA records at the host and
allow inbound TCP ports 80 and 443. Caddy obtains and renews the public TLS
certificate automatically. Its certificates and runtime configuration persist
in the `caddy-data` and `caddy-config` volumes.

With the default `DOMAIN=localhost`, Caddy issues a development certificate
from its internal CA. Browsers will warn until that CA is trusted. To test
without installing the CA:

```bash
curl -k https://localhost/health/ready
```

To trust the local CA, copy its root certificate from the container and import
it into the operating system or browser trust store:

```bash
docker compose cp caddy:/data/caddy/pki/authorities/local/root.crt ./caddy-root.crt
```

## Operations

```bash
docker compose ps
docker compose logs -f caddy
docker compose exec caddy caddy validate --config /etc/caddy/Caddyfile
docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile
```

Do not delete `caddy-data` during routine upgrades: it contains certificate
private keys and other TLS state.
