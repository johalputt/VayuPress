<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)"  srcset="docs/assets/vayupress-mark-dark.png">
    <source media="(prefers-color-scheme: light)" srcset="docs/assets/vayupress-mark-light.png">
    <img src="docs/assets/vayupress-mark-light.png" alt="VayuPress" width="120">
  </picture>
</p>

<h1 align="center">VayuPress</h1>

<p align="center">
  Your website, blog, mail, private chat and Tor onion, in one binary on one server.<br>
  Run from <strong>VayuOS Still Air</strong>, a quiet console that stays out of the way.
</p>

<p align="center">
  <a href="https://github.com/johalputt/vayupress/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/johalputt/vayupress/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/johalputt/vayupress/actions/workflows/security.yml"><img alt="Security" src="https://github.com/johalputt/vayupress/actions/workflows/security.yml/badge.svg"></a>
  <a href="https://github.com/johalputt/vayupress/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/johalputt/VayuPress?sort=semver&color=2d5bd7&label=release"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-27704f"></a>
  <img alt="Telemetry" src="https://img.shields.io/badge/telemetry-none-27704f">
</p>

<p align="center">
  <a href="https://johal.in"><strong>johal.in</strong></a> runs it: 234,615 posts and 13 sites on one small VPS, with its own mail server.
  <a href="#johalin-in-production">The numbers</a>
</p>

<p align="center">
  <img src="docs/screenshots/admin-os-dashboard.png" alt="VayuOS Still Air: Home, with what needs attention, setup steps, visitors and the state of the install" width="920">
</p>

---

**Vayu** is Sanskrit for *wind*: the force that moves everything and belongs to
no one. Point a domain at one server and run one command. You get a website, a
blog at `blog.`, your own mail at `mail.` and private chat at `talk.`, each
with a certificate issued and renewed for you. It all runs as one Go binary with
one SQLite database, sends no telemetry, and has no SaaS behind it. You own
the content, the mailbox, the data and the machine.

## Products

| | What it is | Read more |
|---|---|---|
| **Website** | A business site from eleven templates, edited with a live preview. You choose whether the site or the blog sits at the root. | [A website is a document](docs/adr/ADR-0161-a-website-is-a-document.md) |
| **Blog** | A block editor with lossless Markdown and HTML modes, focus mode, typewriter scrolling, footnotes, diagrams and version diffs. Memberships, newsletters, comments and SEO are built in. | [Block editor](docs/adr/ADR-0092-block-editor-v2-and-low-impact-deploys.md) · [Themes](docs/adr/ADR-0086-theme-whole-site-design.md) |
| **VayuMail** | Your own mail server: SMTP, IMAP and POP3, with DKIM, SPF and DMARC checked live. Every mailbox gets PGP automatically, published by WKD. There are five ways back into a locked mailbox. | [Mail server](docs/adr/ADR-0077-vayumail-outbound-pure-go.md) · [Recovery](docs/MAIL-RECOVERY.md) |
| **VayuMail Mobile** | A pure-Go Android app for your mail and chat. It signs in once, creates its own app password, and decrypts on the device. | [VayuMail-Mobile](https://github.com/johalputt/VayuMail-Mobile) |
| **VayuTalk** | End-to-end-encrypted chat, shared by the web console and the phone. Messages live only in memory and are destroyed once read. | [Architecture](docs/adr/ADR-0131-vayutalk-ephemeral-messaging.md) · [Over Tor](docs/VAYUTALK-ONION-FEDERATION.md) |
| **VayuShield** | A five-layer bot shield that learns offenders and releases them on its own. It never blocks search engines, AI assistants or readers. It cannot absorb a volumetric flood; that needs anycast capacity in front. | [Architecture](docs/adr/ADR-0111-vayushield-bot-protection-and-analytics.md) |
| **VayuAnalytics** | Visitors, funnels, retention and revenue. It uses no cookies, stores no IP address, and needs no consent banner. | [No personal data](docs/adr/ADR-0097-vayuanalytics-no-pii.md) |
| **VayuTor** | A stable Tor v3 `.onion` for every domain, on its own tor. It gets past blocked networks with obfs4 bridges and can mint vanity addresses. | [Onion services](docs/adr/ADR-0138-vayutor-onion-services.md) |
| **Spaces** | Runs the install as a public Clearnet world or an anonymous Tor world. The two have separate databases and nothing crosses between them. | [Two worlds](docs/adr/ADR-0141-vayuos-spaces-clearnet-tor.md) · [Anonymity model](docs/adr/ADR-0143-tor-space-anonymity-model.md) |
| **VayuDomains** | Many independent sites from one process. Each has its own certificate, console, content and analytics. | [Agency hosting](docs/adr/ADR-0152-agency-hosting.md) · [Domain independence](docs/adr/ADR-0153-domain-independence.md) |
| **VayuAPI** | Keys scoped to one `section:action`, each with its own expiry and rate budget, stored only as a hash, and audited. | [Reference](docs/compatibility/vayuapi.md) |
| **VayuMCP** | A built-in MCP server, so Claude or any MCP client can run your site with exactly the rights its key grants. | [Connector](docs/compatibility/mcp.md) · [Buzz](docs/compatibility/buzz.md) |
| **VCB** | The plugin and theme contract, checked by `vayu-compat` using the host's own validator. | [The Bible](docs/compatibility/vcb.md) |
| **VayuFlow** | Automations that are armed step by step and show the exact diff they would apply before they run. | [Architecture](docs/adr/ADR-0151-vayuflow-automation-engine.md) |
| **VayuVeil** | An administrator's view of what this binary exposes: what is reachable, what is observed, and which units are on. | [Architecture](docs/adr/ADR-0150-vayuveil-endpoint-observation-control.md) |
| **Getting paid** | Your own Stripe, PayPal or self-hosted BTCPay (no processor, no KYC). Sell tiers, paywalls, mailboxes, premium addresses and member ads, all through one audited ledger. | [Subscription engine](docs/adr/ADR-0087-subscription-engine-v2.md) |

## VayuOS Still Air

**Still Air** is the design VayuOS is built in, and the name it ships under: a
console that is calm, precise and quiet, where the work is the loudest thing on
the screen.

- **Content first.** Each app opens on what you came for. Help waits behind a
  small ⓘ instead of standing in front of the page.
- **One grammar.** Every page is built from the same few parts: hairline
  sections, rows with their control on the right, figures and disclosures.
- **Colour that means something.** The accent marks the one thing you act on.
  Green, amber and red appear only when a fact calls for them.
- **Two schemes, drawn separately.** Graphite (dark) and Paper (light), each
  with its own tokens.
- **The machine's state is always visible.** The system bar shows the
  operating mode and the world (Clearnet or Tor) on every page. `⌘K` finds any
  page and runs any command.
- **Nothing hidden behind a framework.** It is server-rendered Go and HTMX,
  with one stylesheet and one icon set, served from your origin under a strict
  CSP. It installs as an app, and its service worker caches nothing.
- **Held by tests, not taste.**
  - A design lint walks every console page in a browser and fails on any value
    off the token scale.
  - Twelve surfaces are compared with approved baselines in both schemes.
  - Every text colour is held to WCAG AA against its surface.
  - Lighthouse budgets hold four console pages.

[How the console is built](docs/ADMIN-UI.md)

## Quick start

```bash
curl -sSL https://raw.githubusercontent.com/johalputt/vayupress/main/scripts/deploy-vayupress.sh | bash
```

The installer sets up the binary, the systemd service, Nginx and Let's Encrypt.
It creates an `admin@yourdomain` account with a random password, saved to a
root-only file, and asks for a new password at first sign-in.

Point the apex and `www` at the server for a website. Each subdomain below adds
one product and can be added later:

| Name | CDN proxy | Adds |
|---|---|---|
| `mail.` | **off** | VayuMail |
| `openpgpkey.` | **off** | Automatic PGP key discovery |
| `talk.` | **off** | VayuTalk |
| `mcp.` | **off** | VayuMCP |
| `api.` | **off** | VayuAPI, without challenges |

These must be *DNS only*: mail servers, GnuPG and MCP clients cannot answer a
CDN's bot challenge, and `openpgpkey.` fails silently behind one. A single
8 GB / 4 vCPU / 50 GB VPS runs everything.
[Installation guide](docs/INSTALLATION.md) · [Upgrading](docs/UPGRADING.md) · [Troubleshooting](docs/TROUBLESHOOTING.md)

## johal.in, in production

| | |
|---|---|
| **Version** | VayuPress 3.17.81 |
| **Posts** | 234,615, in one SQLite database |
| **Sites** | 13 hostnames across `johal.in` and `vayupress.com`: 5 blogs, 7 hand-built sites and 1 business template |
| **Also on the box** | The mail server (SMTP, IMAP, POP3, DKIM, WKD) |
| **Hardware** | One Contabo VPS 10 |

| Lighthouse | Mobile | Desktop |
|---|---|---|
| Performance · Accessibility · Best Practices · SEO | 99–100 · 100 · 100 · 100 | 100 · 100 · 100 · 100 |
| Largest Contentful Paint | 1.5–1.7 s | 0.3 s |
| Total Blocking Time | 0–60 ms | 0 ms |
| Time to First Byte, from Iowa | 161 ms | 156 ms |

The install facts were read from johal.in's own API on 26 September 2026. The
Lighthouse figures come from PageSpeed Insights and Cloudflare Synthetic
Monitoring on 27–28 July 2026, at 234,499 posts. Both are refreshed with each
release. An article ships 21.8 KiB of JavaScript, all first-party; there is no
third-party script to block on.
[Benchmarks](docs/BENCHMARKS.md) · [Architecture](docs/ARCHITECTURE.md)

## Showcase

| | |
|---|---|
| ![VayuMail](docs/screenshots/admin-os-vayumail.png) | ![VayuShield](docs/screenshots/admin-os-shield.png) |
| *VayuMail* | *VayuShield: the Aegis layer map* |
| ![Editor](docs/screenshots/admin-os-editor.png) | ![Spaces](docs/screenshots/admin-os-spaces.png) |
| *The block editor* | *Spaces: Clearnet and Tor* |
| ![Homepage](docs/screenshots/homepage.png) | ![Article](docs/screenshots/article-page.png) |
| *The public site* | *An article* |

Every screen is in [`docs/screenshots/`](docs/screenshots/), retaken from a live
instance every two hours.

## Security

- **Strict CSP:** no `unsafe-inline` or `unsafe-eval`, and a nonce on every
  request. Every asset is served from your origin.
- **Encrypted at rest:** PGP private keys and stored secrets use AES-256-GCM.
  Backups are one AES-256-GCM archive, keyed with Argon2id.
- **Plugins:** out-of-process plugins run under seccomp and namespaces, with
  capabilities denied by default.
- **Records:** releases are signed, and there is a write-once audit log.

A commit reaches `main` only through fifteen required CI jobs, including race
tests, a cross-compile matrix and a byte-for-byte reproducible build.

[Security policy](SECURITY.md) · [Threat model](docs/THREAT-MODEL.md) · [Every gate, and what it does not prove](docs/QUALITY-GATES.md) · [Constitution](GOVERNANCE-CONSTITUTION.md) · [Ethics](ETHICS.md)

## Why this exists

I built VayuPress for myself first. Self-hosting hurt, and the pain was not one
dramatic thing but an accumulation: a mail server that needed a weekend and
then broke, six services that each wanted their own database, proxy block,
certificate and upgrade path. That pain is not a law of nature. Nobody had
bothered to make the sovereign option as easy as the rented one. So I made one
binary, one process and one command, and I do not want anyone else to feel what
I felt.

**VayuPress is free for everyone, forever: individuals and large companies
alike.**
- It will not be sold, relicensed, or made source-available.
- There is no paid tier, and no feature is held back.
- The funding goal exists only so the work can become full-time; it is not a
  condition on anything.

[Licensing](docs/LICENSING.md) · [Sustainability](docs/SUSTAINABILITY.md)

> ਨਾਨਕ ਨਾਮ ਚੜ੍ਹਦੀ ਕਲਾ, ਤੇਰੇ ਭਾਣੇ ਸਰਬੱਤ ਦਾ ਭਲਾ
>
> *Nanak Naam Chardi Kala, Tere Bhane Sarbat da Bhala*: may the spirit be ever
> in ascendance; in Your will, may all prosper.

## Documentation

[Changelog](CHANGELOG.md) · [ADRs](docs/adr/) · [API](docs/API-REFERENCE.md) ·
[Operations](docs/OPERATIONS.md) · [Development](docs/DEVELOPMENT.md) ·
[Releases](docs/RELEASES.md) · [EU CRA readiness](docs/CRA-READINESS.md)

## License

Apache License 2.0: see [LICENSE](LICENSE).
- **Use:** anyone may use it, sell services around it, fork it or ship it
  inside a product, with no fee and no permission needed.
- **Copyright:** each contributor keeps copyright in their own work.
  Contributions come in under the
  [Developer Certificate of Origin](https://developercertificate.org/). There
  is no Contributor Licence Agreement, and there will not be one.
- **Permanence:** without a CLA, nobody can build a relicensing business on
  this code, including the author. Every published release stays Apache-2.0
  permanently.

Why Apache-2.0 rather than MIT, and exactly what the pledge does and doesn't
protect, is in [docs/LICENSING.md](docs/LICENSING.md).
