<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)"  srcset="docs/assets/vayupress-mark-dark.png">
    <source media="(prefers-color-scheme: light)" srcset="docs/assets/vayupress-mark-light.png">
    <img src="docs/assets/vayupress-mark-light.png" alt="VayuPress" width="150">
  </picture>
</p>

<h1 align="center">VayuPress</h1>

<p align="center">
  <strong>Your whole online presence — website, blog, private mail, encrypted chat, and one-click Tor .onion — in one sovereign binary.</strong><br>
  One VPS. One process. One control panel. Zero telemetry, zero vendor lock-in, zero SDKs.
</p>

<p align="center">
  <a href="https://github.com/johalputt/vayupress/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/johalputt/vayupress/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/johalputt/vayupress/actions/workflows/security.yml"><img alt="Security" src="https://github.com/johalputt/vayupress/actions/workflows/security.yml/badge.svg"></a>
  <a href="https://github.com/johalputt/vayupress/actions/workflows/dep-freshness.yml"><img alt="Dependency Freshness" src="https://github.com/johalputt/vayupress/actions/workflows/dep-freshness.yml/badge.svg"></a>
  <a href="https://github.com/johalputt/vayupress/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/johalputt/VayuPress?sort=semver&color=0ea5e9&label=release"></a>
  <a href="https://github.com/johalputt/vayupress/stargazers"><img alt="GitHub stars" src="https://img.shields.io/github/stars/johalputt/VayuPress?style=flat&logo=github&color=f5c518"></a>
  <a href="https://go.dev/"><img alt="Go" src="https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-green"></a>
  <img alt="Telemetry" src="https://img.shields.io/badge/telemetry-zero-success">
  <a href="GOVERNANCE-CONSTITUTION.md"><img alt="Constitution" src="https://img.shields.io/badge/constitution-v6.0-blueviolet"></a>
</p>

---

## About

**Vayu** is Sanskrit for *wind* — the invisible force that moves everything and is owned by no one. VayuPress moves your online presence the same way: entirely under your control, seen by no third party.

VayuPress began as a publishing engine. It is now a **complete sovereign platform** —
**ten products in one Go binary**: a business **website**, a **blog**, a private
**PGP email server** (VayuMail) for your own domain with an official **mobile app**,
**ephemeral end-to-end-encrypted chat** (VayuTalk), a self-learning **bot shield**
(VayuShield), **privacy-first analytics** (VayuAnalytics), a fine-grained **scoped
API** (VayuAPI), **one-click Tor `.onion` services** for every domain (VayuTor), a
built-in **MCP server** that connects Claude and any MCP client in one click
(VayuMCP), and a single admin console (**VayuOS**) — that runs on a single modest
VPS. **VayuOS installs as an app** (PWA) on your phone or desktop in one click,
always live via a zero-cache service worker — no store, no build step. You can run
it as **two independently switchable worlds** — a public **Clearnet Space** and a
fully anonymous, web-only **Tor Space** (VayuOS Spaces) — and you can **get paid**
through your own **Stripe, PayPal, or self-hosted BTCPay** (BTC, Monero, Ethereum,
USDT — no processor, no KYC) with built-in memberships, paywalls, a premium mail-ID
marketplace and member ads.

Point a domain at one server, run one install command, and you get:

- a **website** at `yourdomain.com`,
- a **blog** at `blog.yourdomain.com`,
- a **mail server with automatic PGP** at `mail.yourdomain.com`,
- **ephemeral, end-to-end-encrypted chat** at `talk.yourdomain.com`,

each with a free Let's Encrypt certificate issued and renewed for you. No SaaS bill, no analytics harvesting, no plugin marketplace, no credentials on someone else's cloud. **You own the content, the mailbox, the data, and the machine.**

> *Own your content. Own your communication. Own your infrastructure.*

---

## What you get

### 🌐 A real website
Serve a genuine business site at your domain — **11 elegant, modern-minimalist templates** (restaurant, café, shop, portfolio, agency, school, clinic, salon, gym, professional firm, hotel), edited entirely from VayuOS with live preview. You choose the hosting topology (website at the root or the blog at the root); an update never changes it for you.

### ✍️ A Ghost-class blog — with a writer people actually enjoy
A best-in-class **block editor** with whole-document **Markdown** and **HTML** modes (lossless round-trips), drag/drop/paste images or any `https` link, tables, toggles, task lists, math, callouts, code, self-hosted audio/video, Mermaid diagrams rendered server-side, a slash-command palette, live preview, autosave, and version-history diffs. The writing surface is tuned to disappear: **typewriter scrolling** keeps your line centered, a **focus-mode spotlight** dims everything but the block you're in, **paste-as-Markdown** turns a whole pasted draft into real blocks, a **live document outline** tracks your headings with click-to-jump, real **footnotes**, image **captions**, block **duplicate**, and full **keyboard block reordering**. Whole-site **themes** restyle every surface (nav, hero, feed, article, footer) with a live Theme Studio. Multi-author bylines, memberships, paywalls, newsletters, threaded comments, and SEO baked in.

### 🛡️ Built-in bot shield & anti-DDoS (VayuShield "Aegis")
An **enterprise-grade, self-learning bot shield so you never need Cloudflare** — built into the same binary, defending in five layers with **zero operator commands**: an admin-sovereignty lane that keeps **Save and refresh working even during a volumetric flood**, a fixed-memory probabilistic fair-shed that catches spoofed botnets without ever touching a client within its fair budget, a reputation brain that jails offenders in minutes and **forgives automatically**, silent-first proof-of-work challenges that self-calibrate so they never bother real browsers, and optional kernel-level `nftables + XDP` offload. Search engines and AI assistants are always allowed; abuse is shed with a polite `503` (never a `4xx`), so **SEO and real users are structurally protected**. Everything is visible and tunable with no restart from the Bot Shield console and its live Aegis layer map. *([architecture →](docs/adr/ADR-0111-vayushield-bot-protection-and-analytics.md))*

### 📧 A sovereign PGP mail server (VayuMail)
Your own mail server for your domain — **SMTP send + receive, IMAP and POP3**, RFC-6376 **DKIM signing**, direct-to-MX delivery with STARTTLS, automatic **MX / SPF / DKIM / DMARC** records with live DNS health checks, per-mailbox quotas, junk filtering, and a full webmail surface. **PGP is native and automatic** (VayuPGP): keypairs are generated per account, private keys are AES-256-GCM encrypted at rest, and your public keys are published via **Web Key Directory (WKD)** so any client can find them. The composer speaks **PGP/MIME (RFC 3156)**, so an encrypted message can carry **attachments and multiple recipients** — not just a plain-text body. New mail raises a **live topbar bell and desktop notification** (via the service worker, so it works in the installed PWA too) with one click to the mailbox. Mail never leaves your server unencrypted to a third party.

### 💬 Ephemeral end-to-end-encrypted chat (VayuTalk)
Real-time private messaging built into the same binary — a **PGP end-to-end-encrypted chat** for your domain that interoperates seamlessly across the **web console and the mobile app over one shared relay**: a message typed on the web reaches the phone and vice-versa, indistinguishable to the server. Every message is encrypted to the recipient's key, relayed through a **bounded in-memory store that never touches disk**, and **read-destroyed** — it vanishes the moment it is read or when its short TTL (5 min – 1 h) elapses. Out-of-band **safety-number verification** (shown identically on web and app) defeats a man-in-the-middle key swap. The relay auto-serves on a dedicated **`talk.yourdomain.com`** subdomain that bypasses any CDN in front of your site, so the long-lived event stream is never buffered or bot-challenged — provisioned automatically by the installer the moment you point that one DNS record. In the **Tor world** (see VayuOS Spaces below) your chat identity is instead an **anonymous, rotatable code** — not a mailbox — and, opt-in, VayuTalk delivers **onion-to-onion over Tor** between two separate `.onion` installs (guarded `.onion`-only lane, over-Tor key fetch, sender-signature verification, cross-onion read receipts). *([architecture →](docs/adr/ADR-0131-vayutalk-ephemeral-messaging.md) · [Tor federation →](docs/VAYUTALK-ONION-FEDERATION.md))*

### 📱 An official mobile app (VayuMail Mobile)
[**johalputt/VayuMail-Mobile**](https://github.com/johalputt/VayuMail-Mobile) — a pure-Go Android app that reads and sends your PGP mail *and* carries VayuTalk chat from your own domain. Onboarding is **direct-connect**: you enter your domain and sign in once, and the app **provisions a per-device app password automatically** (never your real password, revocable anytime) and auto-configures IMAP/SMTP from VayuPress's first-party autoconfig endpoint — no host, port, or key typing. Encrypted mail is decrypted **on-device** with a synced private key, and VayuTalk chat interoperates with the web console over the same relay. No tracking pixels, no remote content, no telemetry.

### 📊 Privacy-first analytics (VayuAnalytics)
Real product analytics — pageviews, sessions, top pages, referrers, UTM campaigns, custom events, funnels, retention, revenue, and a live visitor panel — stored locally in SQLite. Visitor identity is a **server-side daily-rotating salted hash**: no cookies, no `localStorage`, no IP or User-Agent ever stored, **no consent banner required**, nothing to leak on a database compromise. Visitor country is resolved from an **embedded offline table** — no external GeoIP service, no phone-home.

### 🛠️ One control panel (VayuOS)
Everything above is run from a single, fast, strict-CSP admin at `/os` — dashboard, editor, media library, themes, members, newsletter, mail, **VayuTalk chat**, analytics, **Bot Shield**, SEO, API keys, and one-click **update & encrypted backup**. The dashboard opens on a real **14-day publishing area chart** (server-rendered SVG, hover tooltips, zero JavaScript) and live stat cards; every data table folds into phone-friendly cards on mobile. TOTP two-factor, role-based access, WORM audit log, and an adaptive policy-governed runtime underneath. Built with **HTMX + lightweight hand-written CSS** — no SPA framework, no build step, negligible RAM/CPU.

### 📲 Install VayuOS as an app — one-click PWA
VayuOS is an **installable app**. Open `/os` in any modern browser and it offers **Install VayuOS** — a one-click install that puts the console on your **home screen or desktop** as a standalone, full-screen app on both **mobile and Android/desktop Chromium**; on iPhone/iPad, **Share → Add to Home Screen** does the same. There's a built-in **Install** button in the console top bar too, so it's always one tap away. The installed app opens straight into `/os`, uses the official VayuPress mark as its icon, and launches instantly — no store, no download, no account beyond your normal sign-in. It is **privacy-first and always live**: the app ships a **zero-cache service worker** that never stores a single console response and purges any old cache on upgrade, so an installed VayuOS is **never stale** — every visit shows exactly what the server serves right now, and an update is visible the instant it lands. All same-origin, under the same strict CSP, with a tiny offline notice as the only fallback.

### 🔌 A fine-grained, scoped API (VayuAPI)
Drive the whole platform programmatically — create posts, apply themes, manage domains, read analytics, install plugins, run backups — with **API keys scoped to the exact section and action they need, and nothing more**. Mint a key from a **12 × 6 permission grid** (twelve sections × six actions, written `section:action`), give it an optional hard expiry and a **per-key rate budget**, then rotate, deactivate (reversibly) or revoke it in a click. Keys are **owner-scoped and stored only as a hash** (the raw value is shown once); every call is checked against the key's grant on both the `/api/v1` and `/os` surfaces, metered against its budget (`429` + `Retry-After` when exhausted), and appended to a tamper-evident **audit log**. So a script, a CI job, or an AI agent can update your site autonomously — without ever holding the keys to everything. *([reference →](docs/compatibility/vayuapi.md) · [ADR →](docs/adr/ADR-0134-vayuapi-fine-grained-keys.md))*

### 🧩 An extension contract that guarantees compatibility (VCB)
The **Vayu Compatibility Bible** turns "will this plugin/theme work?" into a checked contract. A developer — or an AI agent — writes a `plugin.json` or `theme.json`, runs **`vayu-compat`**, and knows *before shipping* that every hook, capability, colour, option, sandbox limit, and CSP rule matches what the host actually enforces — because the validator is the **same code the host runs**, so the docs can never drift. Themes that fetch from an external host, plugins that request more than they need, or manifests built against a hook that doesn't exist are refused with a plain, exact reason. Discover the live contract over the API (`GET /api/v1/vcb/contract`) or validate a manifest against a running host (`POST /api/v1/vcb/validate`). *([the Bible →](docs/compatibility/vcb.md) · [ADR →](docs/adr/ADR-0135-vayu-compatibility-bible.md))*

### 🤖 A native Claude / MCP connector (VayuMCP)
Connect VayuPress to an AI assistant the way Claude connects to GitHub — VayuPress serves its **own Model Context Protocol server** from the same binary at `POST /mcp`, so Claude (Desktop, Code, or any MCP client) gets **native tools** to run your site: publish and edit posts, search content, read your site, and more. Auth reuses the **scoped-key** model — a connector can do **exactly** what its key grants and nothing more, so you choose between **full control** (a superuser key, "give Claude the keys") and **limited** (e.g. `posts:write` only); a tool the key doesn't grant is invisible and refused. Every call is rate-limited and written to the same WORM audit log, and there's no new inbound surface beyond one authenticated route. *([the connector →](docs/compatibility/mcp.md) · [ADR →](docs/adr/ADR-0139-vayu-mcp-connector.md))*

### 🧅 Private, censorship-resistant Tor onion services (VayuTor)
Flip one switch in VayuOS and **every domain becomes reachable as its own Tor v3 `.onion`** — alongside its clearnet URL, both serving the same site at once and advertised to Tor Browser via the **`Onion-Location`** header — so visitors reach you with **no ISP, network observer, or third party** able to see who connected or from where. VayuPress runs its **own tor** as an unprivileged child (control-port `ADD_ONION`), pinning each onion's ED25519 key so the address is **stable across restarts** — no root, no `torrc`, no systemd — and even **downloads a current Tor by itself** when the host's is missing or too old, so it works on locked-down and end-of-life servers with **nothing done on the box**. It **beats networks that block Tor** with an automatic ladder (direct → 80/443 → **obfs4 bridges**, the obfs4 transport built in, nothing to install) that routes around IP blocks and DPI; mints **vanity addresses** (a recognisable prefix you choose, brute-forced on the server, no key ever leaving it); reports live **onion health with signed webhook alerts** when onions drop or recover; and **hardens onion responses** (no HSTS, `Referrer-Policy: no-referrer`, no inbound ports). Privacy is structural: the metric is a single count — **no IP** (Tor provides none), no time, path, or user-agent — with per-page counts strictly **opt-in and aggregate-only**. *([architecture →](docs/adr/ADR-0138-vayutor-onion-services.md))*

### 🌐🧅 Two worlds, one binary — VayuOS Spaces (Clearnet & Tor)
Run your platform as **two separate, independently switchable worlds**. A whole-install switch (`VAYUOS_MODE=clearnet|tor`) selects a **Clearnet Space** — your public HTTPS site, global VayuMail, mail-linked VayuTalk, normal analytics — or an anonymous **Tor Space** that is **web-only and fully anonymous**: a Tor-native site on its own `.onion`, webmail-only VayuMail·Tor, and an anonymous rotatable VayuTalk. The Tor world is **anti-leak by construction** — no clearnet callbacks (WKD/gravatar/webmention/MX gated off), external hotlinked images blocked, `img-src 'self' data:` never widened, and http-onion serving with **no CA-TLS, no HSTS, no Secure cookie**. From the clearnet console an operator flips into the Tor world with one click (it manages that world's own data — its Tor domains, blog, mail and chat) and back again. The two worlds keep **separate databases**; accounts, mailboxes, PGP keys and Talk IDs never cross — content moves only through a checksummed, offline-movable `vayupress migrate export|import` bundle. *([architecture →](docs/adr/ADR-0141-vayuos-spaces-clearnet-tor.md))*

### 💳 Turn it on and get paid — Monetization
A complete, **redirect-based** monetization suite (no payment SDK ever embedded — checkout is a top-level redirect, your strict CSP untouched), controlled and audited from one **Monetization control centre** in VayuOS. Take payments through **your own Stripe and PayPal keys** — auto-renewing **PayPal subscriptions** and instant **Stripe one-time** purchases — **or take crypto:** connect a self-hosted **BTCPay Server** and accept **Bitcoin, Monero, Ethereum, and stablecoins** — VayuPress creates the invoice over BTCPay's Greenfield API and a HMAC-verified webhook settles it, with funds landing straight in *your* BTCPay wallet with **no processor, no custody, and no KYC** — the one rail that lets an anonymous or Tor buyer pay without an account. All three sit on one sovereign payments ledger with idempotent fulfilment and verified webhook receipts. Sell **paid membership tiers** that unlock member-only posts; put a **per-post paywall** on any single article (one-time unlock, remembered per member). Turn tiers into **VayuMail mailboxes**: each paid tier provisions a real mailbox with its quota, an auto PGP keypair + WKD, and VayuTalk — plus a **premium / vanity mail-ID marketplace** where reserved and custom addresses are sold (bought → paid entitlement → member claims and sets a password), with operator approve/revoke and a terms-agreement trail. And let members **advertise on your site**: a self-serve "Advertise here" panel takes a flat fee (Stripe, PayPal, BTCPay, or the sovereign direct method) and drops each image ad into an operator **moderation queue** — nothing renders until you approve it. Every paid section — subscriptions, premium IDs, paid posts, member ads — flows through one auditable **Orders** ledger.

---

## Quick start

One command stands up the whole stack — website, blog, and PGP mail — on a fresh VPS:

```bash
curl -sSL https://raw.githubusercontent.com/johalputt/vayupress/main/scripts/deploy-vayupress.sh | bash
```

Or clone and deploy manually:

```bash
git clone https://github.com/johalputt/vayupress.git
cd vayupress
sudo ./scripts/deploy-vayupress.sh
```

The installer provisions the binary, systemd service, Nginx, and Let's Encrypt certificates for your website, blog, and mail hostnames. A fresh install auto-creates an `admin@yourdomain` account (random password, saved to a root-only file) and forces a password change on first sign-in — no extra CLI step.

**Add VayuTalk chat (optional, one DNS record).** VayuTalk works on the main domain out of the box; for the seamless real-time relay behind a CDN, point one `A`/`AAAA` record — `talk.yourdomain.com` → your server, **CDN proxy OFF** (the same "DNS-only" mode you use for `mail.`) — and re-run the installer (or let the next update run it). It adds the subdomain's TLS certificate, writes its Nginx vhost, and advertises it to the app automatically. Nothing else to configure. *(See [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) → "VayuTalk".)*

Runs comfortably on a single **8 GB RAM / 4 vCPU / 50 GB NVMe** VPS.

---

## Why VayuPress

|  | VayuPress | Typical stack |
|---|---|---|
| **What it replaces** | Website builder **+** blog **+** mail provider **+** analytics **+** admin | Four or five separate SaaS bills |
| **Where your data lives** | Your VPS, your SQLite file | Vendor clouds you don't control |
| **Telemetry** | None — verifiable, it's open source | "Anonymized analytics" |
| **Mail** | Your own server, PGP automatic | Google/Microsoft reads the metadata |
| **Private messaging** | Built-in, E2E-encrypted, ephemeral (VayuTalk) | A separate Signal/Slack account & server |
| **Tracking of readers** | Cookieless, no PII, no consent banner | Cookies + third-party pixels |
| **Bot & DDoS protection** | Built-in, self-learning (VayuShield Aegis) | A separate Cloudflare/WAF subscription |
| **Anonymity / Tor** | One-click `.onion` for every domain — stable & **vanity** addresses, **obfs4 bridges** to beat censorship, self-managed tor, health alerts, count-only stats (VayuTor) — **plus** a fully separate, anti-leak **Tor world** (VayuOS Spaces) | Manual torrc surgery, or not at all |
| **Getting paid** | Built-in — your own Stripe/PayPal keys **or self-hosted BTCPay** (BTC/Monero/ETH/USDT, no KYC), paid tiers, per-post paywalls, tier mailboxes, a premium mail-ID marketplace, and member ads, all audited from one panel (Monetization) | A separate Stripe/Memberful/Substack stack — and no crypto option |
| **Dependencies** | One Go binary + SQLite + Nginx | Node, databases, Redis, queues, SDKs |
| **Extensibility** | Sandboxed, capability-gated plugins; a scoped API + a checked compatibility contract (VCB) | Marketplace plugins with full access |
| **Automation / API** | Fine-grained keys scoped to `section:action`, rate-limited & audited (VayuAPI) | All-or-nothing tokens, or none at all |
| **Lock-in** | Open standards, plain export | Proprietary formats, export friction |

---

## One binary, by design

VayuPress is a single Go binary and a single SQLite database. There is no second service to install, secure, or keep alive — search, comments, analytics, mail, and PGP all run in-process.

```text
                         Internet ──HTTPS──▶ Nginx (TLS, static, CSP)
                                                  │ 127.0.0.1:8080
                    ┌─────────────────────────────▼──────────────────────────────┐
                    │                     VayuPress (one Go binary)               │
                    │                                                             │
                    │   VayuShield Aegis (L0 lane · L2 fair-shed · L5 brain)      │
                    │   Website · Blog · Block editor · Themes · Members          │
                    │   VayuMail (SMTP/IMAP/POP3 · DKIM · MX/SPF/DMARC)           │
                    │   VayuTalk (ephemeral E2E chat · SSE relay · read-once)     │
                    │   VayuPGP (keys · WKD)   VayuFind (search)   Analytics      │
                    │   VayuTor (v3 .onion · self-managed tor · obfs4 · vanity)   │
                    │   VayuOS control panel   Newsletter   Media   VayuAPI       │
                    │                                                             │
                    │   ── Platform kernel (immutable) ──                         │
                    │   signing · migrations · outbox · policy · modes · audit    │
                    │                                                             │
                    │                    SQLite (WAL mode)                        │
                    └─────────────────────────────────────────────────────────────┘
```

Under the hood: an **immutable platform kernel** (Ed25519 article signing, checksum-verified migrations, transactional event outbox, WORM audit log, a policy engine and six adaptive system modes), an async SQLite write queue with dead-letter replay, sandboxed out-of-process plugins with seccomp + capability enforcement, and full observability (structured logs, tracing, SLO error budgets). Architecture and every decision are recorded in [`docs/`](docs/) and the [ADR registry](docs/adr/).

---

## Showcase

### Website & blog
![VayuPress homepage](docs/screenshots/homepage.png)
*Public homepage — article grid with tag filtering, dark/light toggle, zero-telemetry footer. Styled entirely from your own origin (strict `style-src 'self'` CSP).*

![VayuPress article](docs/screenshots/article-page.png)
*A rendered article — JSON-LD schema, author/date meta, tag strip, reading time, and zero third-party requests.*

### VayuOS — the single control panel
![VayuOS dashboard](docs/screenshots/admin-os-dashboard.png)
*The dashboard (`/os`) — grouped sidebar, stat cards, publishing-trend sparkline, activity feed, and a `⌘K` command palette.*

> **Install it as an app:** open `/os` and click **Install VayuOS** (or the install button in the top bar; on iOS, Share → Add to Home Screen). It lands on your home screen/desktop as a standalone app, always live via a zero-cache worker.

![VayuOS block editor](docs/screenshots/admin-os-editor.png)
*The block editor — typed-block document rendered server-side through escape + bluemonday, slash-command palette, autosave, live preview, and inline version-history diff.*

![Theme Studio](docs/screenshots/admin-os-theme.png)
*Theme Studio (`/os/theme`) — a preset gallery and design-token editor with instant live preview, compiled to one sovereign stylesheet served from your own origin.*

### 🧅 VayuTor — clearnet and an anonymous Tor world, side by side
![Spaces — Clearnet and Tor](docs/screenshots/admin-os-spaces.png)
*Spaces (`/os/spaces`) — one click runs a **second, fully separate** VayuPress as an anonymous `.onion` world with its own database, accounts and identity. The two share nothing, so their content and logins can never be linked. The page states the honest limit rather than claiming perfect anonymity: both worlds run on the same server, so this separates identity and content, not the machine.*

### 💳 Monetization — the whole revenue engine on one page
![Monetization](docs/screenshots/admin-os-monetization.png)
*Monetization (`/os/monetization`) — cards, PayPal, crypto (BTC/XMR/ETH/USDT) and direct transfer, membership plans, the premium mail-ID marketplace, paid posts and every order. Funds always settle into your own accounts; there is no platform cut and no SDK lock-in.*

### 🛡️ VayuShield — the built-in bot shield
![Bot Shield & Analytics](docs/screenshots/admin-os-shield.png)
*The Bot Shield console (`/os/shield`) — a live Aegis layer map (L0 sovereignty lane · L2 fair-shed · L4 challenges · L5 reputation brain · L1 kernel offload), protection toggles that apply with no restart, learned-signature review queue, and cookieless engagement analytics. Self-learning and self-healing: it protects availability automatically, never blocks a real reader or a search/AI crawler, and needs no Cloudflare.*

![VayuAnalytics](docs/screenshots/admin-os-analytics.png)
*VayuAnalytics — cookieless, no-PII product analytics computed entirely from your local SQLite database.*

### The rest of the ten products

| | |
|---|---|
| ![VayuMail](docs/screenshots/admin-os-vayumail.png) | ![VayuTalk](docs/screenshots/admin-os-vayutalk.png) |
| *VayuMail — a real mail server: SMTP/IMAP/POP3, DKIM, and PGP encryption at rest* | *VayuTalk — end-to-end encrypted chat on your own domain* |
| ![VayuMCP](docs/screenshots/admin-os-connector.png) | ![Members](docs/screenshots/admin-os-members.png) |
| *VayuMCP — a built-in MCP server + OAuth 2.1, so Claude and any MCP client connect in one click* | *Members — tiers, growth, revenue and retention, all from your own database* |
| ![Website](docs/screenshots/admin-os-website.png) | ![Domains](docs/screenshots/admin-os-domains.png) |
| *Website — pages, navigation and the public shell* | *VayuDomains — host several domains from one binary* |

<details>
<summary><strong>More of VayuOS</strong> — posts, media, SEO, security, members and the operator control plane</summary>

| | |
|---|---|
| ![Posts](docs/screenshots/admin-os-posts.png) | ![Media](docs/screenshots/admin-os-media.png) |
| *Post manager — one collapsible card per post* | *Content-addressed media library* |
| ![SEO](docs/screenshots/admin-os-seo.png) | ![Security](docs/screenshots/admin-os-security.png) |
| *SEO readiness dashboard* | *Security & PGP surface (admin-only)* |
| ![Sign-in](docs/screenshots/os-login.png) | ![Settings](docs/screenshots/admin-os-settings.png) |
| *Strict-CSP, self-hosted sign-in* | *Settings — one place, no scattered config* |
| ![Member signup](docs/screenshots/member-signup.png) | ![Plans](docs/screenshots/member-pricing.png) |
| *Branded passwordless member signup* | *Reader-facing plans, aware of who is signed in* |

The adaptive-governance runtime is fully inspectable from inside VayuOS — system modes, the policy provenance inspector, a live runtime-topology graph, the dead-letter replay explorer, the fault manager, and the ADR registry.

| | |
|---|---|
| ![System modes](docs/screenshots/policy-modes.png) | ![Policy inspector](docs/screenshots/policy-inspector.png) |
| ![Runtime topology](docs/screenshots/runtime-topology.png) | ![Replay explorer](docs/screenshots/replay-explorer.png) |
| ![Fault manager](docs/screenshots/fault-manager.png) | ![ADR registry](docs/screenshots/adr-registry.png) |
| ![Governance](docs/screenshots/admin-os-governance.png) | ![Monitoring](docs/screenshots/admin-os-monitoring.png) |

</details>

> Screenshots are regenerated from a live instance by the [screenshots CI workflow](.github/workflows/screenshots.yml).

---

## Security & sovereignty

- **Zero telemetry, zero third-party reader requests.** Strict CSP (no `unsafe-eval`, no `unsafe-inline`, per-request nonces); all assets served same-origin. No CDNs.
- **Encrypted at rest.** PGP private keys and stored third-party secrets are AES-256-GCM encrypted; operator backups are a single AES-256-GCM + Argon2id archive of everything (DB, settings, media, mailboxes, keys).
- **Sandboxed extensibility.** Out-of-process plugins run under seccomp + namespace isolation with deny-by-default capabilities.
- **Governed by construction.** A machine-enforced [Constitution](GOVERNANCE-CONSTITUTION.md), an [Ethical AI Charter](ETHICS.md) (no training on user data, no telemetry), signed releases, and a WORM audit log. AI assistance is strictly opt-in and local-only (Ollama) — nothing leaves your server.

---

## Documentation

- **[CHANGELOG.md](CHANGELOG.md)** — every release and what changed, version by version.
- **[docs/adr/](docs/adr/)** — Architecture Decision Records: every design decision, recorded.
- **[docs/compatibility/vcb.md](docs/compatibility/vcb.md)** — the Vayu Compatibility Bible: how to build a compatible plugin, theme, or tool.
- **[docs/compatibility/vayuapi.md](docs/compatibility/vayuapi.md)** — the API-key, permission, and rate-limit reference.
- **[GOVERNANCE-CONSTITUTION.md](GOVERNANCE-CONSTITUTION.md)** — the binding rules, mechanically enforced by CI.
- **[ETHICS.md](ETHICS.md)** — the Ethical AI Charter.
- **[VayuMail-Mobile](https://github.com/johalputt/VayuMail-Mobile)** — the official mobile mail app.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
