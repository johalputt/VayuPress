# VayuPress Installation Guide

## Requirements

| Resource | Minimum     | Recommended |
|----------|-------------|-------------|
| OS       | Ubuntu 24.04 LTS | Ubuntu 24.04 LTS |
| RAM      | 8 GB        | 12 GB       |
| CPU      | 4 vCPU      | 6 vCPU      |
| Disk     | 50 GB NVMe  | 250 GB NVMe |
| Access   | Root / sudo | Root / sudo |

## Install in one command

You need a fresh **Ubuntu 24.04** server and a **domain name**. That's it.

**Step 1 — point your domain at the server.** In your DNS provider, create **A
records** (add `AAAA` too if your server has IPv6) pointing at your server's IP.

Every record below takes the same value — your server's IP. The column that
actually matters is the last one.

**Required:**

| Type | Name | CDN proxy | What it is |
|---|---|---|---|
| A | `example.com` | on or off | Your website / blog |
| A | `www.example.com` | on or off | `www` redirect |

**Add these for the features you want** — each is independent, and you can add
any of them later:

| Type | Name | CDN proxy | What it is |
|---|---|---|---|
| A | `mail.example.com` | **OFF** | VayuMail — your own mail server |
| A | `openpgpkey.example.com` | **OFF** | VayuPGP key discovery, so encryption is automatic |
| A | `talk.example.com` | **OFF** | VayuTalk — real-time encrypted chat relay |
| A | `mcp.example.com` | **OFF** | VayuMCP — one-click Connect from Claude and any MCP client |
| A | `api.example.com` | **OFF** | VayuAPI — challenge-free REST host for scripts, CI and agents |

Replace `example.com` with your domain.

### Why "CDN proxy OFF" is not optional on those records

This is the single most common way a VayuPress install ends up half-working, and
the failures are quiet ones, so it is worth thirty seconds.

If a CDN such as Cloudflare fronts your domain, its proxy can present a **bot
challenge** — a "checking your browser" page that a human passes without noticing.
Every service above is **machine-to-machine**. A mail server, GnuPG, a CI job and
an MCP client have no JavaScript engine and cannot answer a challenge. What
happens instead:

- **`mail.`** — a CDN HTTP proxy does not carry SMTP or IMAP at all, so mail
  simply does not arrive.
- **`openpgpkey.`** — key discovery fails **silently**, and correspondents quietly
  fall back to unencrypted mail. Nothing appears broken, which is what makes this
  the worst one.
- **`talk.`, `mcp.`, `api.`** — clients receive an HTML challenge page where they
  expected a response, and fail in ways whose error messages point nowhere useful.

In Cloudflare this is the grey cloud, labelled **DNS only**. Your apex and `www`
keep full CDN protection for human traffic — only these direct hosts bypass it,
and each is deliberately narrow (the `api.` and `openpgpkey.` vhosts serve one
path prefix and return 404 for everything else).

### Hosting more than one domain

VayuPress serves many domains from one binary, and **every hosted domain needs
its own records** — the table above is per domain, not per install. In
particular, a certificate is issued **per name**: `openpgpkey.second.example`
cannot be served on `example.com`'s certificate, and a client that reaches it
anyway gets a name mismatch rather than a key.

What each additional domain needs:

| Type | Name | CDN proxy | Needed when |
|---|---|---|---|
| A | `second.example` | on or off | Always |
| A | `www.second.example` | on or off | Always |
| A | `mail.second.example` | **OFF** | That domain has mailboxes |
| A | `openpgpkey.second.example` | **OFF** | That domain has mailboxes |

`talk.`, `mcp.` and `api.` are **install-wide** and live on your primary domain
only — a second copy would have nothing behind it.

Two things gate provisioning for an additional domain, and both are deliberate:

1. **Register it** in **VayuOS → Domains**.
2. **Approve it for sync** there ("Sync now"). A newly registered domain sits on
   manual hold so that an unattended update can never provision a domain behind
   your back. Nothing is issued for a held domain — no certificate, no key
   discovery.

**VayuOS → Operations → Domains & DNS** lists every hosted domain with its
records and live status, and says plainly when a domain is on hold.

### Point the records before you run the installer

The installer provisions a subdomain's certificate and vhost **only once its DNS
resolves**, so a record added afterwards is skipped until you re-run it. That is
not a problem — every update runs the same steps — but it is why a subdomain you
added later appears to do nothing until the next update.

Adding one later:

```bash
sudo bash /path/to/VayuPress/scripts/update-vayupress.sh   # provisions everything pointed
```

**If your DNS is on Cloudflare, you can skip the manual step entirely.** Set
`CF_ZONE_ID` and `CF_API_TOKEN` (token needs `Zone:DNS:Edit`) in
`/etc/vayupress/env`, and the `openpgpkey.` record is created for you — proxy off
— on the next deploy or update.

**Step 2 — run one command** on the server (SSH in as root or a sudo user):

```bash
curl -sSL https://raw.githubusercontent.com/johalputt/VayuPress/main/scripts/deploy-vayupress.sh \
  | sudo DOMAIN=example.com EMAIL=you@example.com bash
```

Replace `example.com` and `you@example.com` with your own. Prefer to be asked
instead? Just run it and it will prompt you:

```bash
curl -sSLo install.sh https://raw.githubusercontent.com/johalputt/VayuPress/main/scripts/deploy-vayupress.sh
sudo bash install.sh          # asks for your domain + email
```

The installer does **everything** automatically — no other terminal commands are
ever needed:

- installs all dependencies (Go, nginx, SQLite, certbot, firewall);
- builds and starts VayuPress as a hardened system service;
- gets **free Let's Encrypt HTTPS certificates** for `example.com`, `www.example.com`
  **and `mail.example.com`** (so the built-in mail server is trusted by phones);
- opens the firewall for web **and** mail, and lets the service run your mail
  ports — so VayuMail works out of the box;
- generates a strong API key for you;
- creates your **administrator account** and prints the password at the end.

**Step 3 — sign in.** When it finishes, it prints something like:

```text
  ── Sign in to VayuOS ─────────────────────────────────────
  URL:      /os/login
  Email:    admin@example.com
  Password: <a strong random password>
  (You'll be asked to set a new password on first login.)
```

Open **`https://example.com/os`**, sign in with that email and password, and
choose your own password when prompted. **From now on you control everything —
posts, themes, mail accounts, updates, backups — from the web console. You never
need the terminal again.** (One-click updates live under **Update & Backup** in
VayuOS.)

> Lost the password? It's also saved on the server at
> `/var/lib/vayupress/initial-admin.txt` (readable by root only), and printed in
> the log: `journalctl -u vayupress | grep -A4 'Default admin'`.

### If a certificate didn't issue

Certificates need your DNS to be pointing at the server first. If you ran the
installer before DNS propagated, just re-run it once DNS is live — it will pick
up where it left off and obtain the certificates.

### Add VayuTalk chat (optional)

**VayuTalk** — ephemeral, end-to-end-encrypted chat that works across the web
console and the mobile app — runs on the main domain out of the box. For the
seamless real-time relay (and to work reliably **behind a CDN** such as
Cloudflare), give it a dedicated subdomain that talks straight to your origin:

1. **Add one DNS record:** `talk.example.com` → your server's IP, with the CDN
   proxy turned **off** (Cloudflare: set it to *DNS only / grey cloud*, exactly
   like your `mail.` record). Your origin IP is already reachable via `mail.`, so
   this exposes nothing new.
2. **Re-run the installer** (or just wait for the next update — every update runs
   the same step):

   ```bash
   sudo bash /path/to/VayuPress/scripts/deploy-vayupress.sh
   # or, to provision only the talk subdomain:
   sudo bash /path/to/VayuPress/scripts/setup-talk-subdomain.sh
   ```

That's the **only** manual step. The installer then automatically adds
`talk.example.com` to your TLS certificate, writes its nginx vhost (relay-only,
with Server-Sent-Events never buffered), sets `VAYUOS_TALK_HOST`, and advertises
the host in autoconfig — so the VayuMail app discovers it and routes its chat
stream there on its own. Until the DNS record exists the step is skipped and the
app keeps using the main domain, so nothing breaks.

**Why the dedicated subdomain?** VayuTalk delivers over a long-lived
Server-Sent-Events stream. A CDN bot-challenge (which a browser can solve but the
mobile app cannot) would block that stream — so the app could send but never
receive. The proxy-off `talk.` subdomain lets the stream reach your origin
unbuffered and unchallenged, while your main domain keeps full CDN protection.
See [`docs/TROUBLESHOOTING.md`](TROUBLESHOOTING.md) → *"VayuTalk"*.

### Connect Claude to your site (optional)

VayuPress has a built-in **OAuth 2.1 MCP connector**, so Claude (Desktop, web, or
any Model Context Protocol client) can connect to your site with a one-click
*sign-in → approve* — no key to copy. Claude connects **machine-to-machine**, so
just like VayuTalk it cannot solve a CDN JavaScript "challenge" page. Behind a CDN
(especially **Cloudflare's free plan**, whose Bot Fight Mode a WAF rule cannot
scope per-path), give the connector its own proxy-off subdomain:

1. **Add one DNS record:** `mcp.example.com` → your server's IP, with the CDN proxy
   turned **off** (Cloudflare: *DNS only / grey cloud*, exactly like `mail.`).
2. **Re-run the installer** (or wait for the next update — every update runs this
   step), or provision just this subdomain:

   ```bash
   sudo bash /path/to/VayuPress/scripts/setup-mcp-subdomain.sh
   ```

The installer then issues a dedicated Let's Encrypt certificate for
`mcp.example.com` (validated straight at your origin, so the proxied apex is never
re-checked), writes its nginx vhost proxying the full app, and the OAuth sign-in +
consent flow work on the subdomain. Verify with `curl -s https://mcp.example.com/health`
(must return JSON, not a challenge page). Then in Claude → **Settings → Connectors
→ Add custom connector**, use **`https://mcp.example.com/mcp`** → **Connect**.

Your main domain keeps full CDN protection; only `mcp.` is direct (VayuShield still
guards the origin, and `/mcp` + `/oauth` are in its bypass). If you are *not* behind
a challenge-mode CDN, you can skip the subdomain and connect Claude straight to
`https://example.com/mcp`. See [`docs/compatibility/mcp.md`](compatibility/mcp.md).

## Direct API host (`api.example.com`)

The REST API (`/api/v1/…`) is called **machine-to-machine** by scripts, CI jobs and
AI agents — none of which can solve a CDN JavaScript challenge either. Behind a
challenge-mode CDN those calls fail, so VayuPress can serve the API on its own
proxy-off subdomain, exactly like `mcp.`:

1. **Add one DNS record:** `api.example.com` → your server's IP, CDN proxy **off**
   (Cloudflare: *DNS only / grey cloud*).
2. **Re-run the installer** (every update runs this step too), or just this subdomain:

   ```bash
   sudo bash /path/to/VayuPress/scripts/setup-api-subdomain.sh
   ```

The installer issues a dedicated Let's Encrypt certificate for `api.example.com`
and writes a **hardened** nginx vhost that proxies **only** `/api` and `/health` —
**everything else returns 404**, so your admin console (`/os`), login and the
OAuth/MCP endpoints are *not* reachable on the direct host. Point clients at
**`https://api.example.com/api/v1/…`** with `Authorization: Bearer <key>` (mint a
scoped key in **VayuOS → API Keys**). Verify with
`curl -s https://api.example.com/health` (JSON, not a challenge page).

**Why this is safe without the CDN in front:** every API call is
key-authenticated, per-key rate-limited and WORM-audited, and VayuShield still
guards the origin — an unauthenticated bot flood only earns cheap `401`s against a
minimal surface. Your main domain keeps full CDN protection for human/browser
traffic. If you are *not* behind a challenge-mode CDN you don't need this at all —
call the API on `https://example.com/api/v1/…` directly.

### Automatic PGP key discovery (`openpgpkey.example.com`)

**Fully automatic — nothing to do if your DNS is on Cloudflare.**

VayuPress generates a PGP keypair for every mailbox and already serves the Web
Key Directory. But PGP clients don't look on your main domain: the WKD spec
defines a dedicated hostname, `openpgpkey.example.com`, and without it your keys
are published where nobody looks.

With it, **GnuPG, Thunderbird and the VayuMail app fetch a correspondent's public
key by themselves** — no key exchange, no attachment, no fingerprint comparison.
That is what makes encryption automatic rather than a thing people mean to get
round to.

The installer provisions it on every deploy *and* every update:

- **If `CF_ZONE_ID` and `CF_API_TOKEN` are set** in `/etc/vayupress/env` (the same
  credentials used for cache purging), the record is **created for you** — proxy
  off, TTL 300 — and the certificate and vhost follow. Zero manual steps.
- **Otherwise**, add one record and re-run: `openpgpkey.example.com` A/AAAA → your
  server, **CDN proxy OFF** (Cloudflare: *DNS only / grey cloud*).

### The one command that needs root

Installing a `systemd` unit requires root, and the VayuPress service runs
unprivileged and deliberately cannot become root — so this is the **single**
privileged step in an otherwise terminal-free product:

```bash
curl -sSL https://raw.githubusercontent.com/johalputt/VayuPress/main/scripts/install-provisioning.sh | sudo bash
```

It installs the provisioning helper, enables a daily sweep, and runs one pass
immediately. It does **not** rebuild the binary, restart the site, or touch the
database, and it is safe to re-run.

After that, **VayuOS → Operations → Domains & DNS** provisions every subdomain on
demand, and a record you point later is picked up on its own. No further terminal
use.

**The proxy must be off.** A WKD fetch is machine-to-machine — GnuPG has no
JavaScript engine and cannot answer a CDN bot-challenge. Behind one, key
discovery fails silently and correspondents quietly fall back to unencrypted
mail, which is the worst possible failure mode: it looks like nothing happened.

**Why exposing it directly is safe:** this is the tightest vhost in the project.
It proxies **only** `/.well-known/openpgpkey/` and returns 404 for everything
else — no `/os`, no login, no API, no static assets. What it serves is *public
key material*, whose entire purpose is to be handed to strangers. There is
nothing to authenticate and nothing to leak.

Verify with:

```bash
gpg --locate-keys you@example.com
```

The script self-checks the WKD policy file after reloading nginx and tells you if
VayuPGP is switched off, which is the one condition that makes it 404 while
looking otherwise healthy.

## Check every record actually works

Run these after the installer. Each one is the check that would have caught the
corresponding failure, and they take under a minute together. **Assume nothing
works until it answers** — most of these fail silently, which is precisely why
they are worth checking rather than trusting.

```bash
D=example.com   # your domain

# Apex + www — expect 200
curl -sS -o /dev/null -w 'apex   %{http_code}\n' "https://$D/"
curl -sS -o /dev/null -w 'www    %{http_code}\n' "https://www.$D/"

# Mail — expect the VayuMail SMTP banner, not a timeout
printf 'QUIT\r\n' | timeout 10 openssl s_client -quiet -starttls smtp \
  -connect "mail.$D:587" 2>/dev/null | head -1

# PGP key discovery — expect a key for a real mailbox on your domain
gpg --locate-keys "you@$D"

# VayuTalk / VayuMCP / VayuAPI — expect JSON, never an HTML challenge page
curl -sS -o /dev/null -w 'talk   %{http_code}\n' "https://talk.$D/health"
curl -sS -o /dev/null -w 'mcp    %{http_code}\n' "https://mcp.$D/health"
curl -sS -o /dev/null -w 'api    %{http_code}\n' "https://api.$D/health"
```

**Reading the results:**

| Symptom | Almost always means |
|---|---|
| Timeout / connection refused | The DNS record isn't pointed, or the installer hasn't run since you added it |
| HTML instead of JSON, or a "checking your browser" page | The **CDN proxy is still on** for that record — switch it to DNS only and re-run the installer |
| Certificate error | The certificate wasn't issued yet; re-run the installer with the record already resolving |
| `gpg --locate-keys` finds nothing | Either `openpgpkey.` isn't pointed, or VayuPGP is switched off in **VayuOS → VayuPGP** |

That last row is worth knowing: the setup script self-checks the WKD policy file
after reloading nginx and will tell you which of the two it is, rather than
leaving you to guess.

### What the certificate covers

The installer requests one certificate covering `example.com`, `www.example.com`,
`mail.example.com` and (when its DNS is pointed) `talk.example.com` — dropping any
name that doesn't yet resolve so a missing optional record never blocks issuance.

`openpgpkey.`, `mcp.` and `api.` each get their **own separate certificate**
instead of being added to that one. This is deliberate: a single certificate with
many names fails as a unit, so one subdomain whose DNS moved would break renewal
for the website and mail too. Separate lineages also keep the shared certificate
well clear of the 100-name limit as an install adds domains.

## Manual / advanced install

Clone the repo and run the same script from disk. Domain, email, worker count
and API key can all be passed in the environment (no file editing needed):

```bash
git clone https://github.com/johalputt/VayuPress.git && cd VayuPress
sudo DOMAIN=example.com EMAIL=you@example.com ./scripts/deploy-vayupress.sh
```

Options:
```bash
sudo ./scripts/deploy-vayupress.sh --dry-run  # validate only, no changes
sudo ./scripts/deploy-vayupress.sh --upgrade  # upgrade an existing install
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
3. Isso (self-hosted comments)
4. Self-hosted fonts (Inter, IBM Plex Mono)
5. VayuPress Go application
6. Systemd services (vayupress, isso)
7. Nginx config with TLS (Let's Encrypt via Certbot)
8. UFW firewall (ports 22, 80, 443)
9. Logrotate, cron jobs (backup, orphan cleanup, restore validation)

> Search is built in (VayuFind, ADR-0101) — there is no external search service
> to install or run.

## Run a Tor Space alongside (VayuOS Spaces)

VayuOS can run two independent worlds at once (ADR-0141): your normal
**Clearnet Space** on its public HTTPS domain, and an anonymous **Tor Space**
served only as a `.onion`. They are separate installs with separate databases —
nothing is shared, so the two can never be correlated.

To stand up a Tor Space next to an existing clearnet install, run one command:

```bash
sudo bash scripts/setup-tor-space.sh
```

This reuses the already-installed `vayupress` binary and Tor daemon, provisions a
persistent `.onion`, writes a second `vayupress-tor` service (its own database,
`VAYUOS_MODE=tor`, no clearnet callbacks, no CA-TLS), starts it, and prints the
onion address. Open `http://<onion>/os` in Tor Browser to create the Tor Space's
first account. The clearnet install is never touched.

Remove just the Tor Space service (keeping its `.onion` and data) with
`sudo bash scripts/setup-tor-space.sh --remove`. Move content between the two
worlds with the offline bundle: `vayupress migrate export --file bundle.vaybundle`.

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
| `DB_PATH`             | `/var/lib/vayupress/vayupress.db`   | SQLite database path               |
| `CACHE_DIR`           | `/var/cache/vayupress`         | Rendered HTML cache directory      |
| `MEDIA_DIR`           | `/var/lib/vayupress/media`     | Editor image uploads (served at `/media/`) |
| `DOMAIN`              | `localhost`                    | Public domain                      |
| `PORT`                | `8080`                         | HTTP listen port                   |
| `WORKER_COUNT`        | `3`                            | Write queue workers                |
| `STORAGE_QUOTA_GB`    | `200`                          | Max storage quota (GB)             |
| `MEDIA_RETAIN_DAYS`   | `365`                          | Days to retain media               |
| `BACKUP_RETAIN_DAYS`  | `30`                           | Days to retain backups             |
| `MAX_REPLAY_COUNT`    | `3`                            | Max dead-letter replay attempts    |
| `WAL_SIZE_THRESHOLD_MB`| `32`                          | WAL size to trigger RESTART checkpoint|
| `VAYU_MAINTENANCE`    | `false`                        | Enable maintenance mode            |
| `VAYU_SELFUPDATE_ENABLED`| `false`                     | Opt-in for `vayupress update apply` (see UPGRADING.md) |
| `VAYU_RELEASE_PUBKEY` | (unset)                        | Hex Ed25519 key the signed apply verifies against |
| `SMTP_HOST`           | (unset)                        | SMTP server for email delivery. Empty = email disabled (no-op) |
| `SMTP_PORT`           | `587`                          | SMTP submission port               |
| `SMTP_USERNAME`       | (unset)                        | SMTP auth username (omit for unauthenticated relays) |
| `SMTP_PASSWORD`       | (unset)                        | SMTP auth password                 |
| `SMTP_FROM`           | `VayuPress <noreply@$DOMAIN>`  | From header / envelope sender      |
| `SMTP_TLS`            | `starttls`                     | `starttls` (587), `ssl` (465), or `none` (trusted localhost) |
| `SCHEDULER_TICK_SEC`  | `60`                           | Scheduled-publish poll interval (seconds); `0` disables |
| `ANALYTICS_RETAIN_DAYS`| `365`                         | Retention window for privacy-first view aggregates |
| `SOCIAL_MASTODON_INSTANCE`| (unset)                    | Mastodon-compatible base URL for auto-posting (e.g. `https://mastodon.social`) |
| `SOCIAL_MASTODON_TOKEN`| (unset)                       | Mastodon app access token (`write:statuses` scope) |
| `VAYU_AI_URL`         | (unset)                        | Local Ollama base URL for the AI writing assistant (e.g. `http://localhost:11434`) |
| `VAYU_AI_MODEL`       | `llama3.2`                     | Ollama model name for the assistant |
| `STRIPE_WEBHOOK_SECRET`| (unset)                       | Stripe webhook signing secret for paid-member upgrades (optional) |

### Email delivery (Tier 1)

VayuPress sends email over plain SMTP using only the Go standard library — no
third-party SDKs, no hosted APIs, no telemetry. Set `SMTP_HOST` (plus
credentials) to enable:

- **Double opt-in confirmations** are emailed automatically on newsletter
  subscribe.
- **Broadcasts** to all confirmed subscribers via
  `POST /api/v1/admin/newsletter/broadcast` (`{subject, text, html}`), each with
  an auto-appended unsubscribe link.

When `SMTP_HOST` is empty, every email call is a safe no-op: subscriber and
comment flows keep working, delivery is simply skipped and audit-logged.

### Memberships & paywalls (Tier 2)

Turn readers into members and gate premium content — sovereign, no payment SDK
embedded.

- **Passwordless member login.** Readers request a magic link
  (`POST /members/login` or `/api/v1/members/login`), receive a one-time emailed
  link to `/members/verify`, and get a hardened `HttpOnly` session cookie. No
  reader passwords are ever stored. (Requires SMTP — see email setup.)
- **Per-article access levels.** Set `public`, `members`, or `paid` via
  `PUT /api/v1/admin/articles/{slug}/access`. Non-authorised readers see a
  preview plus a sign-in CTA instead of the full body; gated articles bypass the
  static cache so access is re-checked per request.
- **Tiers.** `GET /api/v1/admin/members` lists members;
  `PUT /api/v1/admin/members/{email}/tier` sets `free`/`paid` manually.
- **Optional Stripe upgrades.** Set `STRIPE_WEBHOOK_SECRET` and point a Stripe
  webhook at `POST /api/v1/stripe/webhook`. The signature is verified
  (HMAC-SHA256 over `t.payload`); on `checkout.session.completed` the customer's
  member is upgraded to `paid`. VayuPress embeds no Stripe SDK — it only reacts
  to the signed webhook.

### AI writing assistant (Tier 2)

An opt-in, **sovereign** writing assistant that talks to a LOCAL,
operator-run [Ollama](https://ollama.com) server — nothing is ever sent to a
hosted third-party model. Set `VAYU_AI_URL` (and optionally `VAYU_AI_MODEL`) to
enable. Operations via `POST /api/v1/admin/ai/assist` (`{op, text}`):
`summarize`, `improve`, `titles`, `seo`, `continue`. Probe availability at
`GET /api/v1/admin/ai/status`.

The assistant only *suggests* — it never auto-edits content, consistent with the
project's "no autonomous actions" ethics charter.

### Social auto-posting (Tier 2)

When `SOCIAL_MASTODON_INSTANCE` + `SOCIAL_MASTODON_TOKEN` are set, each newly
published article is automatically shared to your Mastodon-compatible server
(Mastodon, Pleroma, Akkoma). A single app access token is all that's needed — no
OAuth redirect flow. Sharing is async and best-effort; a failed toot never
affects publishing, and an idempotency key prevents duplicate posts on retry.

### Migrating from Ghost & WordPress (Tier 2)

Two built-in importers move content off the most common platforms with no
external tooling:

```bash
# Ghost — Settings → Export → download the JSON
vayupress migrate ghost --file ghost-export.json --dry-run   # preview
vayupress migrate ghost --file ghost-export.json             # import

# WordPress — Tools → Export → All content (WXR/XML)
vayupress migrate wordpress --file wordpress-export.xml --dry-run
vayupress migrate wordpress --file wordpress-export.xml
```

Both preserve titles, slugs, publish dates, tags/categories, and draft status
(`--skip-drafts` is on by default), sanitise HTML through the same policy as the
write queue, and dedupe by slug so re-running is safe.

### Privacy-first analytics (Tier 2)

Cookieless, consent-free page-view counting that stores **no IP addresses, no
user agents, no cookies, no fingerprints, and no per-visitor rows** — only daily
aggregate counts per path and per referrer host. There is nothing in the schema
that can identify a reader. Read the rollup at
`GET /api/v1/admin/analytics?days=30&limit=20`. Aggregates older than
`ANALYTICS_RETAIN_DAYS` are pruned daily.

### Outbound webhooks (Tier 2)

Register endpoints that receive a signed JSON POST when content changes —
integrate with Zapier, n8n, Make, or any custom service. Each delivery carries
`X-VayuPress-Signature: sha256=<hmac>` over the raw body (per-hook secret) plus
`X-VayuPress-Event`. Subscribe to `article.created.v1`, `article.updated.v1`,
`article.deleted.v1`, or `*`. Bounded retry/backoff; every attempt is recorded.

```bash
# Register a webhook (secret returned once, on creation)
curl -X POST https://$DOMAIN/api/v1/admin/webhooks \
  -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/hook","events":["article.created.v1","article.updated.v1"]}'
```

Manage with `GET /api/v1/admin/webhooks`,
`DELETE /api/v1/admin/webhooks/{id}`, and inspect delivery history at
`GET /api/v1/admin/webhooks/{id}/deliveries`.

### Multi-author accounts & password login (Tier 1)

VayuPress supports per-author accounts with email + password sign-in, in
addition to the legacy single API key. Passwords are hashed with Argon2id;
login sessions are stored server-side in SQLite (only the SHA-256 of each token
is persisted) and carried in a hardened `HttpOnly`, `SameSite=Lax` cookie.

Bootstrap the first admin from the CLI (the only path that needs no existing
session):

```bash
vayupress user add alice@example.com 'a-strong-password' Alice --admin
vayupress user list
vayupress user passwd alice@example.com 'new-password'
vayupress user delete bob@example.com
```

Sign in at `/admin/v2/login`. Admin-role users can also manage accounts over the
API: `GET/POST /api/v1/admin/users`, `DELETE /api/v1/admin/users/{email}`. The
existing API-key path keeps working unchanged — admin pages accept **either** a
valid API key **or** a login session.

### Image optimization (Tier 1)

Editor image uploads are automatically optimized using a **stdlib-only** pipeline
(no libvips, no CGO, no third-party scaling libraries). PNG and JPEG uploads
wider than 1600px are downscaled proportionally with area-averaging resampling
and re-encoded; the smaller of the optimized/original bytes always wins.
Animated GIF and WebP pass through untouched to preserve animation and format.
The upload response now includes `width` and `height`.

### Scheduled publishing (Tier 1)

Stage future-dated posts with `POST /api/v1/admin/schedule`
(`{slug, title, content, tags[], publish_at}` where `publish_at` is RFC3339). A
durable SQLite-backed ticker promotes each post through the normal
render → index → cache pipeline when its time arrives. Posts staged while the
server was down are caught up on the next startup tick. List with
`GET /api/v1/admin/schedule`; cancel with `DELETE /api/v1/admin/schedule/{id}`.

## Docker

A multi-stage `Dockerfile` and `docker-compose.yml` ship in the repo root for a
container deployment. The image compiles the CGO/SQLite binary, then runs it as
an unprivileged user on a minimal Debian-slim base with a built-in healthcheck.

```bash
cp .env.example .env
# edit .env: set a strong API_KEY (openssl rand -hex 32) and your DOMAIN
docker compose up -d --build
```

The Compose deployment includes Caddy as its TLS-terminating reverse proxy.
Caddy starts before VayuPress, obtains and renews certificates automatically,
and is the only service that publishes host ports. See the root
[`CADDY.md`](../CADDY.md) for local-certificate trust and operational commands.

If you deploy the binary without Compose, it listens on plain HTTP `:8080` and
expects a trusted reverse proxy in front that sets `X-Forwarded-For`. A minimal
nginx server block for that deployment style is:

```nginx
server {
    listen 443 ssl http2;
    server_name example.com;

    ssl_certificate     /etc/letsencrypt/live/example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/example.com/privkey.pem;

    client_max_body_size 12m;   # headroom for 8 MB editor image uploads

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
    }
}
```

Persistent state lives in two named volumes: `vayupress-data`
(`/var/lib/vayupress` — SQLite DB **and** uploaded media) and `vayupress-cache`
(`/var/cache/vayupress` — rendered HTML, sitemap, feed). Back up the former; the
latter is regenerable.

### Backup (Docker)

```bash
# Hot backup of the SQLite DB + media to a tarball on the host:
docker run --rm -v vayupress-data:/data -v "$PWD":/backup debian:bookworm-slim \
  tar czf /backup/vayupress-$(date +%F).tar.gz -C /data .
```

For online, WAL-safe backups and restore, the bundled `vayu-backup` tool and
[docs/operations/backup-restore.md](operations/backup-restore.md) remain the
recommended path.

## Upgrade

```bash
cd vayupress
git pull origin main
sudo ./scripts/deploy-vayupress.sh --upgrade
```

The `--upgrade` flag preserves existing secrets and data. For container
deployments, rebuild and recreate: `docker compose up -d --build`. See
[docs/UPGRADING.md](UPGRADING.md) for the signed self-update path.

## Uninstall

```bash
sudo systemctl stop vayupress isso
sudo systemctl disable vayupress isso
sudo rm -f /etc/systemd/system/vayupress.service
sudo rm -f /etc/systemd/system/isso.service
# Optionally remove data:
# sudo rm -rf /var/lib/vayupress /var/cache/vayupress /var/log/vayupress
```

## Documentation

Every guide, operations runbook, the security model and all Architecture Decision
Records ship **inside the binary** and are served by your own install at
**`https://<your-domain>/docs`** — no `docs.` subdomain and no DNS record to add.
The canonical project copy is at `https://vayupress.com/docs`.

## Support

support@vayupress.com — https://vayupress.com/docs
