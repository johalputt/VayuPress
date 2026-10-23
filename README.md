<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)"  srcset="docs/assets/vayupress-mark-dark.png">
    <source media="(prefers-color-scheme: light)" srcset="docs/assets/vayupress-mark-light.png">
    <img src="docs/assets/vayupress-mark-light.png" alt="VayuPress" width="150">
  </picture>
</p>

<h1 align="center">VayuPress</h1>

<p align="center">
  <strong>Your whole online presence — website, blog, private mail, encrypted chat and a Tor .onion — in one sovereign binary.</strong><br>
  One VPS. One process. One SQLite file. One console. No telemetry, no SaaS, no SDKs.
</p>

<p align="center">
  <a href="https://github.com/johalputt/vayupress/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/johalputt/vayupress/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/johalputt/vayupress/actions/workflows/security.yml"><img alt="Security" src="https://github.com/johalputt/vayupress/actions/workflows/security.yml/badge.svg"></a>
  <a href="https://github.com/johalputt/vayupress/actions/workflows/dep-freshness.yml"><img alt="Dependency Freshness" src="https://github.com/johalputt/vayupress/actions/workflows/dep-freshness.yml/badge.svg"></a>
  <a href="https://github.com/johalputt/vayupress/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/tag/johalputt/VayuPress?sort=semver&color=2d5bd7&label=release"></a>
  <a href="https://github.com/johalputt/vayupress/stargazers"><img alt="GitHub stars" src="https://img.shields.io/github/stars/johalputt/VayuPress?style=flat&logo=github&color=f5c518"></a>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/johalputt/VayuPress?logo=go&logoColor=white&color=00ADD8"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-27704f"></a>
  <img alt="Telemetry" src="https://img.shields.io/badge/telemetry-none-27704f">
  <a href="GOVERNANCE-CONSTITUTION.md"><img alt="Constitution" src="https://img.shields.io/badge/constitution-v6.0-blueviolet"></a>
</p>

<p align="center">
  <img src="docs/screenshots/admin-os-dashboard.png" alt="VayuOS Still Air: Home, with what needs attention, setup steps, visitors and the state of the install" width="920">
</p>

---

## About

> **Live install:** **[johal.in](https://johal.in)** — **234,997 posts**, **14 sites** and a full mail server, all on **one Contabo VPS 10**, on VayuPress 3.18.11. [See the numbers →](#running-in-production--johalin)

**Vayu** is Sanskrit for *wind* — the invisible force that moves everything and is owned by no one. VayuPress moves your online presence the same way: entirely under your control, seen by no third party.

VayuPress began as a publishing engine. It is now a **complete sovereign platform in one Go binary**:

- a business **website** and a **blog** with its own reading theme, **Halcyon**;
- a private **mail server** for your own domain, with **PGP made automatic** (VayuMail) and an official **mobile app**;
- **end-to-end-encrypted chat** that lives only in memory (VayuTalk);
- a self-learning **bot shield** (VayuShield) and **cookieless analytics** (VayuAnalytics);
- a **Tor `.onion`** for every domain (VayuTor), and a whole second, anonymous **Tor world** (Spaces);
- **many independent sites** from one process (VayuDomains);
- a **scoped API** (VayuAPI), a built-in **MCP server** for Claude and any MCP client (VayuMCP), and a **checked plugin and theme contract** (VCB);
- **automation that shows its diff before it runs** (VayuFlow), an **endpoint observation console** (VayuVeil), and a full **monetization** engine on your own Stripe, PayPal or BTCPay;

— all run from one console, **VayuOS Still Air**, which installs as an app on your phone or desktop.

Point a domain at one server, run one install command, and you get:

- a **website** at `yourdomain.com`,
- a **blog** at `blog.yourdomain.com` (or at the root — you choose),
- a **mail server with automatic PGP** at `mail.yourdomain.com`,
- **end-to-end-encrypted chat** at `talk.yourdomain.com`,

each with a free Let's Encrypt certificate issued and renewed for you. No SaaS bill, no analytics harvesting, no credentials on someone else's cloud. **You own the content, the mailbox, the data and the machine.**

> *Own your content. Own your communication. Own your infrastructure.*

---

## Why this exists

I built VayuPress for myself first.

Self-hosting hurt. Not in any single dramatic way — it was the accumulation. A mail server that
needed a weekend and then broke. Six services that each wanted their own database, their own
reverse-proxy block, their own certificate, their own upgrade path. A stack that worked until one
piece moved and then did not. Every guide assuming you already knew the part you did not know.

That pain is not a law of nature. It is a consequence of nobody having bothered to make the
sovereign option as easy as the rented one, because there is no money in making people
independent. So I made the thing I needed: **one binary, one process, one command**, and the
whole stack works.

**I do not want anyone else to feel what I felt.** That is the entire goal. Not profit, not an
exit, not a company.

### What that means in practice

**VayuPress is free for everyone, forever — individuals and large companies alike.** A big
technology company can run this, build on it, and ship it inside a product without paying anyone
anything. That is not a loophole to be closed later; it is the point. A licence that carves out
the powerful is still a licence with a gatekeeper, and I did not build this to become one.

**It will not be sold, relicensed, or made source-available.** Not for money, not for a hosted
edition, not for an acquisition. The reasoning and the structure that backs it are in
[docs/LICENSING.md](docs/LICENSING.md).

**There is no paid tier and no feature held back.** Every install gets the same binary.

### About the funding goal

There is a goal of **USD 5,000 per month** in [docs/SUSTAINABILITY.md](docs/SUSTAINABILITY.md).
It exists for exactly one reason: I currently work on VayuPress part-time, and that figure is
what would let me work on it full-time.

**It is not a condition on anything.** If it is never reached, VayuPress stays free, stays
Apache-2.0, and stays maintained part-time. Nothing gets sold. The values came first and the
funding question does not get to revise them.

> ਨਾਨਕ ਨਾਮ ਚੜ੍ਹਦੀ ਕਲਾ, ਤੇਰੇ ਭਾਣੇ ਸਰਬੱਤ ਦਾ ਭਲਾ
>
> *Nanak Naam Chardi Kala, Tere Bhane Sarbat da Bhala* — may the spirit be ever in ascendance;
> in Your will, may all prosper.

---

## What you get

### ✍️ A blog people enjoy reading — Halcyon

A new site starts on **Halcyon**, VayuPress's own theme: a reading serif (Newsreader) on warm paper, a dusk that is never black, one quiet accent and hairline rules instead of boxes. **Home** is a front page with numbered topic desks, a river, or an index by month; an **article** is a centred column or a margin carrying its facts; a **topic** is an index by month, newest first or **most read** over the last 7 or 30 days, with a search that stays inside the topic. **Readers choose** their own face, size, width and light or dark, saved on their device only and applied before the page is drawn. An outline marks where they are, a selection toolbar quotes and links, and on a phone they read with a dock. **Nothing scrolls sideways** — code wraps, and on a phone a wide table becomes labelled blocks — proven on johal.in's own posts at 320 px. Text contrast, control names, keyboard focus and reflow are measured on every page type, in both schemes, against WCAG 2.2 AA. The stylesheet is 9.4 KB and the script 3.8 KB gzipped, with its fonts served from your origin and no third-party request. Comments, most read, pinned and related posts, sign-in, sign-up and the member pages are all in the same design, and **every style is a choice in Theme Studio**, whose preview is the real page by page, device and scheme. *([Halcyon →](docs/adr/ADR-0166-a-theme-may-bring-its-layout.md))*

### 🖋️ A writer that disappears while you write

A **block editor** with whole-document **Markdown** and **HTML** modes that round-trip losslessly; tables, toggles, task lists, math, callouts, code, footnotes, captions, self-hosted audio and video; **Mermaid diagrams rendered on the server**, and **SVG diagrams drawn inline** — sanitised to a closed allowlist, so a drawing shows and nothing in it can run. A slash-command palette, autosave, live preview and version-history diffs. **Typewriter scrolling** keeps your line centred, a **focus spotlight** dims everything but the block you are in, a pasted Markdown draft becomes real blocks, and a live outline tracks your headings. Multi-author bylines, memberships, paywalls, newsletters, threaded comments with moderation, and an optional **AI draft** from a local Ollama or a provider you configure — always a suggestion you insert, never published on its own. Garbled characters in imported posts are found and listed for their author to restore or keep; nothing is changed unasked.

### 🔎 Made to be found — and quoted

Every post tells search and answer engines what is true of it as **one structured-data graph built by the server**: its author and their page, the site and publisher, its topic, tags and length, its breadcrumb, and the questions its FAQ answers. Its description is its opening prose cut at a word. Tags are **topics**: one address each, a few to a post, counted from the published posts. **`/llms.txt`** follows the convention for AI assistants: a summary, the site's pinned and newest posts each with a one-line summary, and its topics. Sitemaps, an RSS feed, IndexNow and webmentions are built in. Whether links to other sites carry `nofollow` is **one switch for the whole site**, posts and comments alike.

### 🌐 A real website

A genuine business site at your domain from **eleven templates** — restaurant, café, shop, portfolio, agency, school, clinic, salon, gym, professional firm and hotel — edited from VayuOS with a live preview, or **your own hand-built site**, uploaded as a bundle or followed from a GitHub repository folder. You choose whether the website or the blog sits at the root, and an update never changes it for you. *([a website is a document →](docs/adr/ADR-0161-a-website-is-a-document.md))*

### 📧 A sovereign PGP mail server (VayuMail)

Your own mail server for your domain — **SMTP send and receive, IMAP and POP3**, **DKIM signing**, direct-to-MX delivery over STARTTLS, and **MX, SPF, DKIM and DMARC** records with live DNS checks — plus per-mailbox quotas, filters, an auto-responder, junk filing and full webmail. **PGP is native and automatic** (VayuPGP): every account gets a keypair, its private key encrypted at rest with AES-256-GCM, and its public key published by **Web Key Directory** so any client finds it. The composer speaks **PGP/MIME (RFC 3156)**, so an encrypted message carries attachments and several recipients. Subjects and names in any language read as written, everywhere. The Mailboxes page puts **new mail first**, across every domain, and follows it live. New mail raises a **bell and a desktop notification** — through the service worker, so it reaches the installed app too.

**A forgotten mailbox password is not a dead end.** There are five ways back in — single-use recovery codes, a verified recovery address off the install, a still-syncing trusted device, an administrator-approved one-time link, and a host break-glass command — all running one reset pipeline that revokes every app password and session, holds queued outbound mail, and writes an append-only audit trail. The public flow never confirms whether an address exists, and no reset mints a session or reveals a password. *([recovery guide →](docs/MAIL-RECOVERY.md) · [ADR →](docs/adr/ADR-0144-vayumail-account-recovery.md))*

### 📱 An official mobile app (VayuMail Mobile)

[**johalputt/VayuMail-Mobile**](https://github.com/johalputt/VayuMail-Mobile) — a pure-Go Android app that reads and sends your PGP mail and carries VayuTalk, from your own domain. You enter your domain and sign in once; the app **provisions its own per-device app password** (never your real password, revocable at any time) and configures IMAP and SMTP from VayuPress's own autoconfig endpoint. Encrypted mail is decrypted **on the device**. No tracking pixels, no remote content, no telemetry.

### 💬 End-to-end-encrypted chat that lives only in memory (VayuTalk)

Real-time **PGP end-to-end-encrypted chat** for your domain, shared by the web console and the phone over one relay: a message typed on the web reaches the phone and back, indistinguishable to the server. Every message is encrypted to its recipient, held in a **bounded in-memory store that never touches disk**, and **destroyed the moment it is read**; an unread one is let go after 24 hours by default (anywhere from 5 minutes to 7 days, as the operator sets). **Safety numbers** shown identically on web and phone defeat a key swap. The relay serves on its own **`talk.yourdomain.com`**, past any CDN, so the live stream is never buffered or challenged. In the Tor world your chat identity is an **anonymous, rotatable code**, and opt-in, VayuTalk delivers **onion to onion** between two separate `.onion` installs. *([architecture →](docs/adr/ADR-0131-vayutalk-ephemeral-messaging.md) · [over Tor →](docs/VAYUTALK-ONION-FEDERATION.md))*

### 🛡️ A built-in bot shield (VayuShield "Aegis")

A self-learning bot shield in the same binary, in five layers that need **no operator command**: an **admin-sovereignty lane** that keeps the console answering while the public site is under application-layer load; a fixed-memory **probabilistic fair-shed** that catches spoofed botnets without touching a client inside its fair budget; a **reputation brain** that jails offenders in minutes and **forgives on its own**; silent-first **proof-of-work challenges** that calibrate themselves so real browsers are not bothered; and optional kernel-level **nftables and XDP offload**. **41 request-inspection rules** and published **datacenter ranges** (AWS, Google Cloud, DigitalOcean) feed a score that never blocks on its own. Search engines and AI assistants are always allowed; abuse is turned away with `429` or `503` and `Retry-After`, so search ranking and real readers are protected by construction. A **posture report** judges every control by what is actually enforcing, not by which switches are on. *([architecture →](docs/adr/ADR-0111-vayushield-bot-protection-and-analytics.md))*

**What it does not do, stated plainly:** it will not absorb a volumetric flood. Every layer above runs after a completed TCP handshake on your uplink, so a flood large enough to saturate that uplink is decided upstream of your server, by your provider's routers, and no software on the host can change that. VayuShield handles the abuse a publisher actually meets — scrapers, credential stuffing, comment spam, AI crawlers, application-layer floods. For volumetric resilience you need anycast capacity: a network in front, or several nodes of your own. The posture report keeps this as a permanent, visible fail rather than hiding it.

### 📊 Analytics with no personal data (VayuAnalytics)

Pageviews, sessions, top pages, referrers, UTM campaigns, custom events, funnels, retention, revenue and a live visitor panel — stored in your own SQLite. A visitor is a **server-side hash with a salt that rotates daily**, so visits cannot be linked across days: no cookies, no `localStorage`, no IP address or user agent stored, **no consent banner required**, and nothing to leak if the database is ever taken. Country comes from an **embedded offline table** — no GeoIP service, no phone-home — and a browser that sends Global Privacy Control is not counted. *([no personal data →](docs/adr/ADR-0097-vayuanalytics-no-pii.md))*

### 🛠️ One console — VayuOS Still Air

Everything above is run from one console at `/os`, built in a design called **Still Air**: calm, precise and quiet, where the work is the loudest thing on the screen. **Home** opens on what needs you, the steps left to finish setting up, your visitors and the state of the install. Every page is built from the same few parts — hairline sections, rows with their control on the right, figures and disclosures — and colour appears only when a fact calls for it. The system bar shows the operating mode and the world (Clearnet or Tor) on every page, and **`⌘K` finds any page and runs any command**. Two schemes, Graphite and Paper, are drawn separately. TOTP two-factor, roles, a write-once audit log, and an install that reports its own mode — normal, degraded, read-only, recovery, maintenance or quarantined. It is **server-rendered Go and HTMX**, one stylesheet and one icon set from your origin, with no SPA framework and no build step. *([how the console is built →](docs/ADMIN-UI.md))*

### 📲 Install VayuOS as an app

Open `/os` in any modern browser and it offers **Install VayuOS** — a one-click install onto your home screen or desktop as a standalone app; on iPhone and iPad, **Share → Add to Home Screen**. The installed app opens straight into the console and launches instantly — no store, no download. Its service worker **caches no console response**, so an installed VayuOS is never stale: every visit shows what the server serves now.

### 🌐 Many sites, one binary — VayuDomains and agency hosting

Point a second domain at the same server and it is **a site, not a copy of yours**: its own certificate, its own console, its own branding, content and analytics, and its own choice of blog, business template or hand-built bundle — changeable later without a migration. johal.in's install serves **fourteen hostnames across two apex domains from one process and one database**, every certificate renewing without a restart. There is no per-site process, config file or cron. *([agency hosting →](docs/adr/ADR-0152-agency-hosting.md) · [domain independence →](docs/adr/ADR-0153-domain-independence.md) · [one console per site →](docs/adr/ADR-0154-one-console-per-site.md))*

### 🧅 A Tor onion for every domain (VayuTor)

Flip one switch and **every domain becomes reachable as its own Tor v3 `.onion`**, alongside its clearnet address, advertised to Tor Browser with the **`Onion-Location`** header. VayuPress runs **its own tor** as an unprivileged child over the control port (`ADD_ONION`), pinning each onion's key so the address is **stable across restarts** — no root, no `torrc`, no systemd — and it **fetches a current Tor itself** when the host's is missing or too old. It gets past **networks that block Tor** with an automatic ladder (direct, then 80/443, then **obfs4 bridges**, with the obfs4 transport built in); mints **vanity addresses** with a prefix you choose, without a key ever leaving the server; and sends **signed webhook alerts** when an onion drops or recovers. Its only metric is a count. *([onion services →](docs/adr/ADR-0138-vayutor-onion-services.md))*

### 🌐🧅 Two worlds, one binary — Spaces

Run the install as **two separate worlds**. A **Clearnet Space** is your public HTTPS site, VayuMail and VayuTalk. A **Tor Space** is **web-only and anonymous**: a site on its own `.onion`, webmail-only mail, and anonymous chat. The Tor world is **built not to leak**: no clearnet callbacks (WKD, Gravatar, webmentions and MX are gated off), no hotlinked images, and http-onion serving with no CA certificate, no HSTS and no Secure cookie. One click moves the console into the Tor world and back. The two worlds keep **separate databases**, so accounts, mailboxes, keys and chat identities never cross; content moves only as a checksummed `vayupress migrate export|import` bundle. Both run on the same server, so this separates identity and content, not the machine — and the console says so. *([two worlds →](docs/adr/ADR-0141-vayuos-spaces-clearnet-tor.md) · [anonymity model →](docs/adr/ADR-0143-tor-space-anonymity-model.md))*

### 🔌 A fine-grained, scoped API (VayuAPI)

Drive the whole platform from code with **keys scoped to the exact section and action they need** — a **12 × 6 grid** of sections (posts, themes, design, plugins, domains, analytics, media, comments, members, mail, settings, backup) and actions (read, write, delete, apply, install, export), written `section:action`. A key gets an optional expiry and its **own rate budget**; it is stored **only as a hash**, shown once, and can be rotated, paused or revoked in a click. Every call is checked against the key's grant, metered (`429` and `Retry-After` when spent) and written to the audit log — so a script, a CI job or an AI agent can run your site without ever holding the keys to everything. *([reference →](docs/compatibility/vayuapi.md) · [ADR →](docs/adr/ADR-0134-vayuapi-fine-grained-keys.md))*

### 🤖 A native Claude / MCP connector (VayuMCP)

VayuPress serves its **own Model Context Protocol server** at `/mcp`, with **OAuth 2.1** discovery and client registration, so Claude (Desktop, Code, or the web) and any MCP client connect in one click. More than forty tools publish and edit posts, search content, apply themes, manage hosted sites and their files, read analytics and VayuShield's state, and more — each one granted only as far as the connecting key's scopes allow: give it the keys, or only `posts:write`. A tool the key does not grant is invisible and refused, and every call is rate-limited and audited. The agents in a **[Buzz](https://github.com/block/buzz)** workspace connect over the same endpoint. *([the connector →](docs/compatibility/mcp.md) · [Buzz →](docs/compatibility/buzz.md) · [ADR →](docs/adr/ADR-0139-vayu-mcp-connector.md))*

### 🧩 An extension contract that is checked (VCB)

The **Vayu Compatibility Bible** turns "will this plugin or theme work?" into a checked contract. A developer — or an AI agent — writes a `plugin.json` or `theme.json`, runs **`vayu-compat`**, and knows before shipping that every hook, capability, colour, option, sandbox limit and CSP rule matches what the host enforces, because the validator **is the host's own code**. The live contract is at `GET /api/v1/vcb/contract`, and a running host validates a manifest at `POST /api/v1/vcb/validate`. *([the Bible →](docs/compatibility/vcb.md) · [ADR →](docs/adr/ADR-0135-vayu-compatibility-bible.md))*

### ⚙️ Automation that shows its work before it runs (VayuFlow)

Schedules, events and manual triggers drive **flows**: ordered steps over what the platform already does — content, mail, outbound calls, model calls — each **armed explicitly** before it can act. A flow can be **dry-run** to show the exact diff it would apply before anything is written. Runs carry idempotency keys and survive a crash, a durable inbox holds event triggers, and the whole engine is **inert in a Tor Space** rather than quietly reaching the clearnet. *([architecture →](docs/adr/ADR-0151-vayuflow-automation-engine.md))*

### 🔭 Know what your endpoints are doing (VayuVeil)

An administrator's **observation console** for what this binary exposes: what is reachable, what is observed, and which units are switched on — read-only where reading is enough, with the controls that change something kept explicit and audited. *([architecture →](docs/adr/ADR-0150-vayuveil-endpoint-observation-control.md))*

### 💳 Turn it on and get paid — Monetization

A complete, **redirect-based** monetization suite: no payment SDK is ever embedded, so your strict CSP stays untouched. Take payments through **your own Stripe and PayPal** — auto-renewing subscriptions and one-time purchases — **or crypto through your own BTCPay Server** (Bitcoin, Monero, Ethereum, stablecoins), settled by a verified webhook straight into *your* wallet with **no processor, no custody and no KYC**: the one rail an anonymous or Tor buyer can pay through. Sell **membership tiers** that unlock member-only posts, a **paywall** on any single post, **tiers that come with a real VayuMail mailbox** (with its quota, PGP key and chat), a **premium mail-address marketplace**, and **member ads** that wait in your moderation queue until you approve them. Every sale runs through one auditable **orders ledger**. *([subscription engine →](docs/adr/ADR-0087-subscription-engine-v2.md))*

---

## Quick start

One command stands up the whole stack — website, blog and PGP mail — on a fresh VPS:

```bash
curl -sSL https://raw.githubusercontent.com/johalputt/vayupress/main/scripts/deploy-vayupress.sh | bash
```

Or clone and deploy by hand:

```bash
git clone https://github.com/johalputt/vayupress.git
cd vayupress
sudo ./scripts/deploy-vayupress.sh
```

The installer sets up the binary, the systemd service, Nginx and Let's Encrypt certificates, and installs Go itself when the host's is older than the one the project needs. A fresh install creates an `admin@yourdomain` account with a random password, saved to a root-only file, and asks for a new password at first sign-in — no extra step.

**DNS records — point these before you install.** The apex and `www` are all a website needs. Each subdomain below unlocks one product, is independent of the others, and can be added later (re-run the installer, or let the next update pick it up).

| Name | CDN proxy | Unlocks |
|---|---|---|
| `yourdomain.com`, `www.` | on or off | Website and blog |
| `mail.yourdomain.com` | **off** | VayuMail — your own mail server |
| `openpgpkey.yourdomain.com` | **off** | VayuPGP key discovery — makes encryption automatic |
| `talk.yourdomain.com` | **off** | VayuTalk — the encrypted chat relay |
| `mcp.yourdomain.com` | **off** | VayuMCP — one-click Connect from Claude and any MCP client |
| `api.yourdomain.com` | **off** | VayuAPI — a challenge-free REST host for scripts, CI and agents |

**The proxy column is the one people get wrong.** Every service on those subdomains is machine-to-machine: a mail server, GnuPG, a CI job and an MCP client have no JavaScript engine and cannot answer a CDN's bot challenge. Behind one they fail, and `openpgpkey.` fails *silently* — key discovery stops and correspondents quietly fall back to unencrypted mail. In Cloudflare that is the grey cloud, **DNS only**. Your apex and `www` keep full CDN protection for people.

If your DNS is on Cloudflare, set `CF_ZONE_ID` and `CF_API_TOKEN` in `/etc/vayupress/env` and the `openpgpkey.` record is created for you, proxy off, on the next update. Per-record checks and what each failure looks like are in the *([installation guide →](docs/INSTALLATION.md))*.

A single **8 GB RAM / 4 vCPU / 50 GB NVMe** VPS runs everything.
[Upgrading](docs/UPGRADING.md) · [Troubleshooting](docs/TROUBLESHOOTING.md) · [Operations](docs/OPERATIONS.md)

---

## Why VayuPress

|  | VayuPress | Typical stack |
|---|---|---|
| **What it replaces** | Website builder **+** blog **+** mail provider **+** analytics **+** chat **+** admin | Four to six separate SaaS bills |
| **Where your data lives** | Your VPS, your SQLite file | Vendor clouds you don't control |
| **Telemetry** | None — verifiable, it's open source | "Anonymized analytics" |
| **Mail** | Your own server, PGP automatic | A provider that reads the metadata |
| **Private messaging** | Built in, end-to-end encrypted, held only in memory | A separate Signal or Slack account |
| **Tracking of readers** | Cookieless, no personal data, no consent banner | Cookies and third-party pixels |
| **Bot protection** | Built in and self-learning, with its limits stated | A separate WAF subscription |
| **Anonymity / Tor** | A `.onion` per domain — stable, vanity, past blocks with obfs4 — plus a separate, anti-leak Tor world | Hand-edited `torrc`, or nothing |
| **Getting paid** | Your own Stripe, PayPal or BTCPay; tiers, paywalls, mailboxes, premium addresses, member ads, one ledger | A separate membership platform, and no crypto |
| **AI assistants** | A built-in MCP server; each connection gets exactly the rights its key grants | An all-or-nothing token, or nothing |
| **Dependencies** | One Go binary + SQLite + Nginx | Node, databases, Redis, queues, SDKs |
| **Extensibility** | Sandboxed plugins; a checked contract (VCB); a scoped API | Marketplace plugins with full access |
| **Lock-in** | Open standards, plain export | Proprietary formats, export friction |

---

## One binary, by design

VayuPress is a single Go binary and a single SQLite database. There is no second service to install, secure or keep alive — search, comments, analytics, mail, chat and PGP all run in-process.

```text
                         Internet ──HTTPS──▶ Nginx (TLS, compression)
                                                  │ 127.0.0.1:8080
                    ┌─────────────────────────────▼──────────────────────────────┐
                    │                     VayuPress (one Go binary)               │
                    │                                                             │
                    │   VayuShield Aegis (L0 lane · L2 fair-shed · L5 brain)      │
                    │   Website · Blog (Halcyon) · Block editor · Members         │
                    │   VayuMail (SMTP/IMAP/POP3 · DKIM · MX/SPF/DMARC)           │
                    │   VayuTalk (end-to-end chat · in-memory relay · read-once)  │
                    │   VayuPGP (keys · WKD)   VayuFind (search)   Analytics      │
                    │   VayuTor (v3 .onion · own tor · obfs4 · vanity)            │
                    │   VayuOS console · VayuAPI · VayuMCP · VayuFlow · VayuVeil  │
                    │                                                             │
                    │   ── Platform kernel ──                                     │
                    │   migrations · outbox · write queue · modes · audit         │
                    │   page cache (stale-while-revalidate · paced warmer)        │
                    │                                                             │
                    │                    SQLite (WAL mode)                        │
                    └─────────────────────────────────────────────────────────────┘
```

Under the hood: a platform kernel — checksum-verified migrations, a transactional event outbox, a write-once audit log and six system modes — an asynchronous SQLite write queue with dead-letter replay, and a page cache built to stay fast on a large archive: pages and listing cards are kept on disk, a stale page is served at once and redrawn off the request path, a paced warmer rebuilds what an edit or a release made stale at the speed the host can spare, browsers revalidate each page (an unchanged one costs a `304`), and the whole cache is **one switch** in Settings. Plugins run out of process under seccomp, namespaces and a capability allowlist. Every decision is recorded in the [ADR registry](docs/adr/) and the [architecture notes](docs/ARCHITECTURE.md).

---

## Running in production — [johal.in](https://johal.in)

Benchmarks are easy to stage. This is a live install you can open right now and check yourself.

| | |
|---|---|
| **Site** | **[johal.in](https://johal.in)** — a working blog, publicly reachable |
| **Published posts** | **234,997**, in one SQLite database |
| **Hardware** | A single **Contabo VPS&nbsp;10** — one box, nothing else |
| **Running on it** | **14 sites** across `johal.in` and `vayupress.com` (3 blogs, 10 hand-built sites, 1 business template), every certificate live, **+** the mail server (SMTP, IMAP, POP3, DKIM, WKD) |
| **VayuShield** | Protecting, with 41 inspection rules and three datacenter range feeds |
| **Version** | VayuPress 3.18.11 |

### Google PageSpeed Insights

| | Mobile | Desktop |
|---|---|---|
| **Home** — Performance · Accessibility · Best Practices · SEO | 98 · **100** · **100** · **100** | **100** · **100** · **100** · **100** |
| **An article** — the same four | **100** · **100** · **100** · **100** | **100** · 97 · **100** · **100** |
| Largest Contentful Paint | 1.5–1.6 s | **0.3 s** |
| Total Blocking Time | **0 ms** | **0 ms** |
| Cumulative Layout Shift | 0–0.001 | 0–0.003 |
| JavaScript sent, all first-party | home 18.0 KiB · article 23.3 KiB | the same |

### Cloudflare synthetic monitoring — the server-side number

A second, independent Lighthouse run (Cloudflare Synthetic Monitoring, from Iowa, USA) reports the metric that matters most for the architecture:

| Metric | Mobile | Desktop |
|---|---|---|
| **Time to First Byte** | **161 ms** | **156 ms** |
| Time to Interactive | 1,492 ms | **391 ms** |
| Total Blocking Time | 29 ms | **0 ms** |
| Cumulative Layout Shift | **0** | **0** |

**Over 230,000 posts on SQLite, on one small VPS.** "SQLite doesn't scale" is the most common objection to this architecture, and this is the answer to it: a quarter of a million articles from a single file, on hardware that costs less per month than most managed-database add-ons. WAL mode, covering indexes, an asynchronous write queue and stored listing cards do the work: a topic page that is not yet cached draws in **6–16 ms** on a test copy of the same size, because it reads two hundred small cards instead of two hundred post bodies.

**Fourteen sites on one box.** The same VPS terminates TLS for fourteen hostnames, runs the mail server and serves the blog — from one binary and one database. No managed Postgres, no Redis, no separate mail provider, no CDN origin shield, and no second process per site. One binary hosting fourteen independent sites is not a bigger version of hosting one: it is the case where a shared database, a shared TLS store and a shared control plane either hold or do not.

**0 ms blocking time is a design outcome, not a tuning trick.** There is no third-party analytics, tag manager, font CDN or tracking pixel to block on, because the platform ships its own analytics and serves every byte from your origin. The pages are fast largely because nothing else is invited onto them.

> The version, sites, post count and shield state were read from johal.in's own API on 23 October 2026. The PageSpeed figures are from 27 September 2026, the home page and an article (`/vayupress-for-privacy`); the article's 97 on desktop was small touch targets. The time to first byte is from Cloudflare Synthetic Monitoring, Iowa, 27–28 July 2026. Your numbers will depend on your content, VPS and network. [Benchmarks →](docs/BENCHMARKS.md)

---

## Showcase

### The public site — Halcyon
![VayuPress homepage](docs/screenshots/homepage.png)
*Home in Halcyon — the site's name, the newest post, Most read over the last 7 or 30 days, and a footer that switches light and dark. Everything is served from your own origin.*

![VayuPress article](docs/screenshots/article-page.png)
*An article — its structured data, author and date, topics, reading time, and no third-party request.*

### VayuOS Still Air — the one console
![VayuOS Home](docs/screenshots/admin-os-dashboard.png)
*Home (`/os`) — what needs you, the steps left to finish setting up, visitors, the state of the install, and a `⌘K` command bar.*

![VayuOS block editor](docs/screenshots/admin-os-editor.png)
*The block editor — server-rendered blocks, a slash-command palette, autosave, live preview and version diffs.*

![Theme Studio](docs/screenshots/admin-os-theme.png)
*Theme Studio (`/os/theme`) — Halcyon's options with the real page as the preview, by page, device and scheme.*

### 🧅 Clearnet and an anonymous Tor world, side by side
![Spaces — Clearnet and Tor](docs/screenshots/admin-os-spaces.png)
*Spaces — one click runs a second, fully separate VayuPress as an anonymous `.onion` world with its own database, accounts and identity. The page states the limit plainly: both worlds run on the same server, so this separates identity and content, not the machine.*

### 💳 The revenue engine on one page
![Monetization](docs/screenshots/admin-os-monetization.png)
*Monetization (`/os/monetization`) — cards, PayPal, crypto and direct transfer, membership plans, the premium mail-address marketplace, paid posts and every order. Funds settle into your own accounts; there is no platform cut.*

### 🛡️ VayuShield
![VayuShield](docs/screenshots/admin-os-shield.png)
*The VayuShield console (`/os/shield`) — the Aegis layer map, protection switches that apply without a restart, the learned-signature review queue, and cookieless engagement figures.*

### The rest of the products

| | |
|---|---|
| ![VayuMail](docs/screenshots/admin-os-vayumail.png) | ![VayuTalk](docs/screenshots/admin-os-vayutalk.png) |
| *VayuMail — SMTP, IMAP and POP3, DKIM, and PGP* | *VayuTalk — end-to-end encrypted chat on your own domain* |
| ![VayuMCP](docs/screenshots/admin-os-connector.png) | ![Members](docs/screenshots/admin-os-members.png) |
| *VayuMCP — Claude and any MCP client connect in one click* | *Members — tiers, growth, revenue and retention* |
| ![Website](docs/screenshots/admin-os-website.png) | ![Domains](docs/screenshots/admin-os-domains.png) |
| *Website — pages, navigation and the public shell* | *VayuDomains — many sites from one binary* |
| ![VayuAnalytics](docs/screenshots/admin-os-analytics.png) | ![Posts](docs/screenshots/admin-os-posts.png) |
| *VayuAnalytics — computed from your own database* | *Posts* |

<details>
<summary><strong>More of VayuOS</strong> — media, SEO, security, settings, members and the operator's control plane</summary>

| | |
|---|---|
| ![Media](docs/screenshots/admin-os-media.png) | ![SEO](docs/screenshots/admin-os-seo.png) |
| *The media library* | *SEO readiness* |
| ![Security](docs/screenshots/admin-os-security.png) | ![Settings](docs/screenshots/admin-os-settings.png) |
| *Security and PGP (administrator only)* | *Settings — one place, no scattered config* |
| ![Sign-in](docs/screenshots/os-login.png) | ![Member signup](docs/screenshots/member-signup.png) |
| *The sign-in, self-hosted under a strict CSP* | *Passwordless member sign-up* |
| ![Plans](docs/screenshots/member-pricing.png) | ![Monitoring](docs/screenshots/admin-os-monitoring.png) |
| *Reader-facing plans* | *Monitoring* |

The install's own state is inspectable from inside VayuOS — its system modes, the runtime topology, the dead-letter replay explorer, the fault manager and the ADR registry.

| | |
|---|---|
| ![System modes](docs/screenshots/policy-modes.png) | ![Runtime topology](docs/screenshots/runtime-topology.png) |
| ![Replay explorer](docs/screenshots/replay-explorer.png) | ![Fault manager](docs/screenshots/fault-manager.png) |
| ![Governance](docs/screenshots/admin-os-governance.png) | ![ADR registry](docs/screenshots/adr-registry.png) |

</details>

> Screenshots are retaken from a fresh install by the [screenshots workflow](.github/workflows/screenshots.yml) every two hours, and on demand.

---

## Security & sovereignty

- **No telemetry, no third-party requests for your readers.** Every asset — scripts, stylesheets, fonts, images — is served from your origin. The only exception is one you choose: switch on Google AdSense and its script comes with it.
- **A strict Content Security Policy.** Scripts run only with a per-request nonce or a known hash; there is no `unsafe-eval` and no inline script. Stylesheets come only from your origin; inline style *attributes* are allowed, and style cannot run code.
- **Encrypted at rest.** PGP private keys and stored third-party secrets (payment, AI and DNS keys) are AES-256-GCM encrypted. An operator backup is a single AES-256-GCM archive of everything — database, settings, media, mailboxes, keys — keyed with Argon2id.
- **Sandboxed extensibility.** Out-of-process plugins run under seccomp (seven Linux architectures), namespaces and a deny-by-default capability set.
- **Recorded and signed.** A write-once audit log; releases signed with cosign and an RFC 3161 timestamp; a [Constitution](GOVERNANCE-CONSTITUTION.md) checked by CI and an [Ethical AI Charter](ETHICS.md): no training on your data, no telemetry, and AI only ever as a suggestion you accept.

[Security policy](SECURITY.md) · [Threat model](docs/THREAT-MODEL.md) · [Attack surfaces](docs/security/attack-surfaces.md)

---

## What every change passes before it ships

There is no separate QA step. A commit reaches `main` only by passing **sixteen required CI jobs** — one of which, the cross-compile matrix, is itself seven parallel builds — and `ci-pass` re-checks every one by name. A job that ran without being listed there would be a job that cannot fail the build. Browser tests, race detection, Lighthouse and the supply-chain scan run alongside.

**The Go code itself**

| Gate | What it catches |
|---|---|
| `go vet`, `gofmt -l` | Suspicious constructs; unformatted source |
| `staticcheck` | Dead stores, impossible conditions, misused stdlib |
| `golangci-lint run` (v2, zero issues) | The aggregate linter set, errcheck included |
| `gosec -severity high -confidence high` | Injection, weak crypto, hardcoded credentials |
| `govulncheck` | Known CVEs on code paths actually reachable from this binary |
| `go build -race` + `go test -race` | Data races, in unit and integration runs |
| `go test -shuffle=on` | Tests that only pass in their usual order |
| `go test ./internal/archcheck/` | Layering violations: which package may import which |
| `go test ./internal/compat/` | Golden-file breaks in the plugin and theme contract (VCB) |
| deadcode gate | Newly unreachable code, against a committed baseline |
| **cross-compile matrix** | **Architecture-specific compile errors, on seven Linux ABIs** |
| **`go mod tidy` drift, `go mod verify`** | **Undeclared or unused dependencies; module contents that no longer match `go.sum`** |
| **reproducible build** | **Two cold builds of identical source that are not byte-identical** |

**Everything around the code**

| Gate | What it catches |
|---|---|
| trufflehog (filesystem mode) | Committed secrets |
| shellcheck, markdownlint | Broken shell, malformed docs |
| **heredoc audit** | **An unescaped backtick inside an unquoted heredoc — live command substitution in a file that is supposed to be config text** |
| SPDX + license check | A source file with no licence header |
| ADR completeness, required docs | A design decision that shipped unrecorded |
| Governance, ethics, security-policy and community checks | Drift between the Constitution and the repository |
| source-sync | A missing entry point or required package, or a module that does not build |
| **release-metadata consistency** | **`.release-version` and the top `CHANGELOG.md` section disagreeing** |

**In the browser.** Playwright drives the real console and the real public site on a fresh install: every console page is walked by a design lint that fails on any value off the token scale; sixteen surfaces, console and public site, are compared with approved pictures; every text colour is held to WCAG AA against its surface; and Halcyon is checked on every page type for sideways scrolling, overlays, reflow at 320 px, contrast and keyboard focus. Lighthouse holds the home page, a post and four console pages to their budgets.

The release-metadata check exists because of how releases work here: pushing `.release-version` *is* the release. Nothing downstream re-derives the version. Without that check a version can be bumped and announced while the notes describe a different release — or a changelog can claim fixes shipped when no build was ever cut.

The cross-compile matrix earned its place the same way. Every other Go gate runs on the runner's own `linux/amd64`, so nothing asked whether the source compiled anywhere else. It did not: `internal/sandbox` named `syscall.SYS_EPOLL_WAIT` in architecture-neutral code, and the arm64 and riscv64 ABIs have never defined it. ARM VPSes are squarely this product's audience. The gate then failed on its own first run, twice, on code written to fix that — a 32-bit `int` cannot hold `SECCOMP_RET_KILL_PROCESS`, and `SYS_SOCKET` does not exist on 386, which multiplexes through `socketcall(2)`. Both were in test files, which `go build` does not compile and `go vet` does.

**Every gate is mutation-tested.** A gate that has not been seen to fail on the thing it forbids has proved nothing, so each new check is run once against a deliberately broken copy of the code it guards, and has to fail at its own assertion. Three ways that has caught a false green: a test that judged output by calling the function under test, so loosening the code loosened the test; a seed that tripped several rules at once, so no single rule was ever deciding; and two guards for one outcome, where "it failed" never said which fired.

**What these gates do not prove.** They run on `linux/amd64`; the cross-compile matrix proves the other six architectures *compile*, not that they were tested, and releases remain a single native build. The reproducibility gate proves two cold builds of the same source are byte-identical; `go.mod` pins the Go toolchain and releases build with `GOTOOLCHAIN=auto`, so a tag decides its own compiler. The C side — SQLite is linked statically — still depends on the release runner's gcc and libc, which are not yet pinned. An earlier version of this section claimed builds were *not* reproducible; that was wrong, it was published before being checked properly, and the controlled test is now a gate so the answer stops depending on who measured it. [Every gate, and what it does not prove →](docs/QUALITY-GATES.md)

---

## Documentation

- **[CHANGELOG.md](CHANGELOG.md)** — every release and what changed, version by version.
- **[docs/adr/](docs/adr/)** — Architecture Decision Records: every design decision, and what has since replaced it.
- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — how the binary is put together.
- **[docs/API-REFERENCE.md](docs/API-REFERENCE.md)** and **[docs/compatibility/vayuapi.md](docs/compatibility/vayuapi.md)** — the API, its keys, permissions and rate limits.
- **[docs/compatibility/mcp.md](docs/compatibility/mcp.md)** — connecting Claude and any MCP client.
- **[docs/compatibility/vcb.md](docs/compatibility/vcb.md)** — the Vayu Compatibility Bible: how to build a compatible plugin, theme or tool.
- **[docs/MAIL-RECOVERY.md](docs/MAIL-RECOVERY.md)** — VayuMail account recovery: what to enrol before anyone is locked out, and every way back in.
- **[docs/INSTALLATION.md](docs/INSTALLATION.md)**, **[docs/UPGRADING.md](docs/UPGRADING.md)**, **[docs/OPERATIONS.md](docs/OPERATIONS.md)** — running an install.
- **[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)** and **[docs/RELEASES.md](docs/RELEASES.md)** — building and releasing it.
- **[docs/LICENSING.md](docs/LICENSING.md)** — why Apache-2.0, why no CLA, and how the licence could change in future.
- **[docs/CRA-READINESS.md](docs/CRA-READINESS.md)** — EU Cyber Resilience Act: scope assessment and gap analysis.
- **[GOVERNANCE-CONSTITUTION.md](GOVERNANCE-CONSTITUTION.md)** and **[ETHICS.md](ETHICS.md)** — the binding rules, and the Ethical AI Charter.
- **[VayuMail-Mobile](https://github.com/johalputt/VayuMail-Mobile)** — the official mobile mail app.

---

## License

**Apache License 2.0** — see [LICENSE](LICENSE). **Free for anyone, for anything, forever.** Use
it, sell services around it, fork it, ship it inside a commercial product. No fee, no permission,
no registration. Self-hosters carry no obligations whatsoever: run it, modify it, keep your
changes private — Apache-2.0 asks nothing of you, which is exactly why it was chosen over a
copyleft licence.

**Apache-2.0 permanently, unconditionally.** No reciprocal move, no source-available move, no
dual licence, no open-core split — and no condition attached: not funding, not adoption, not
acquisition. Every release already published is irrevocably free as a completed legal fact, not
a promise. Nobody has to pay anything to use VayuPress, now or ever.

Every source file carries an `SPDX-License-Identifier`, third-party components are listed in
[NOTICE](NOTICE), and the reasoning — including what structurally backs that pledge — is in
[docs/LICENSING.md](docs/LICENSING.md).

Apache-2.0 rather than MIT for the patent grant (§3): every contributor licenses their patents,
and that licence terminates for anyone who sues over them. MIT is silent on patents, which is a
real exposure for a codebase implementing DKIM, OpenPGP, MCP and OAuth.

**Copyright is held by each contributor over their own work.** It is not assigned to anyone.
Contributions are accepted under the [Developer Certificate of Origin](https://developercertificate.org/)
with `git commit -s`, and **there is no Contributor Licence Agreement — there will not be one.**

That is a deliberate structural commitment, not an oversight. A CLA concentrates copyright in one
party, and only a party holding the copyright can sell proprietary licences to it. Every
well-known relicensing of a community project into a paid product was built on one. Without a
CLA that business cannot be constructed here — by anyone, including the author — and **every
release already published stays under Apache-2.0 permanently**, forkable from its tag no matter
what this repository does later.

[docs/LICENSING.md](docs/LICENSING.md) sets out precisely what that protects, what it does not,
and the routes by which the licence could change in future.
