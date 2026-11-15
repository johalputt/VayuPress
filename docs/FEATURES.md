# What VayuPress does

Everything one install of VayuPress provides, app by app. The
[README](../README.md) is the short version; each section here links to the
guide or the decision record behind it. What is production, beta or
experimental is in the README's [maturity table](../README.md#how-ready-each-part-is).

## ✍️ A blog people enjoy reading — Halcyon

A new site starts on **Halcyon**, VayuPress's own theme: a reading serif (Newsreader) on warm paper, a dusk that is never black, one quiet accent and hairline rules instead of boxes. **Home** is a front page with numbered topic desks, a river, or an index by month; an **article** is a centred column or a margin carrying its facts; a **topic** is an index by month, newest first or **most read** over the last 7 or 30 days, with a search that stays inside the topic. **Readers choose** their own face, size, width and light or dark, saved on their device only and applied before the page is drawn. An outline marks where they are, a selection toolbar quotes and links, and on a phone they read with a dock. **Nothing scrolls sideways** — code wraps, and on a phone a wide table becomes labelled blocks — proven on johal.in's own posts at 320 px. Text contrast, control names, keyboard focus and reflow are measured on every page type, in both schemes, against WCAG 2.2 AA. The stylesheet is 9.4 KB and the script 3.8 KB gzipped, with its fonts served from your origin and no third-party request. Comments, most read, pinned and related posts, sign-in, sign-up and the member pages are all in the same design, and **every style is a choice in Theme Studio**, whose preview is the real page by page, device and scheme. *([Halcyon →](adr/ADR-0166-a-theme-may-bring-its-layout.md))*

## 🖋️ A writer that disappears while you write

A **block editor** with whole-document **Markdown** and **HTML** modes that round-trip losslessly; tables, toggles, task lists, math, callouts, code, footnotes, captions, self-hosted audio and video; **Mermaid diagrams rendered on the server**, and **SVG diagrams drawn inline** — sanitised to a closed allowlist, so a drawing shows and nothing in it can run. A slash-command palette, autosave, live preview and version-history diffs. **Typewriter scrolling** keeps your line centred, a **focus spotlight** dims everything but the block you are in, a pasted Markdown draft becomes real blocks, and a live outline tracks your headings. Multi-author bylines, memberships, paywalls, newsletters, threaded comments with moderation, and an optional **AI draft** from a local Ollama or a provider you configure — always a suggestion you insert, never published on its own. Garbled characters in imported posts are found and listed for their author to restore or keep; nothing is changed unasked.

## 🔎 Made to be found — and quoted

Every post tells search and answer engines what is true of it as **one structured-data graph built by the server**: its author and their page, the site and publisher, its topic, tags and length, its breadcrumb, and the questions its FAQ answers. Its description is its opening prose cut at a word. Tags are **topics**: one address each, a few to a post, counted from the published posts. **`/llms.txt`** follows the convention for AI assistants: a summary, the site's pinned and newest posts each with a one-line summary, and its topics. Sitemaps, an RSS feed, IndexNow and webmentions are built in. Whether links to other sites carry `nofollow` is **one switch for the whole site**, posts and comments alike.

## 🌐 A real website

A genuine business site at your domain from **eleven templates** — restaurant, café, shop, portfolio, agency, school, clinic, salon, gym, professional firm and hotel — edited from VayuOS with a live preview, or **your own hand-built site**, uploaded as a bundle or followed from a GitHub repository folder. You choose whether the website or the blog sits at the root, and an update never changes it for you. *([a website is a document →](adr/ADR-0161-a-website-is-a-document.md))*

## 📺 A YouTube channel as a blog

Paste a channel, confirm it, and **every video becomes a post by itself**, the back catalogue and every new one: its title, its description with its links and timestamps, its hashtags as tags, its picture in your media library, its date, and the player, which loads from YouTube only when a reader presses play. Shorts are drawn upright. Each channel is checked every 15 minutes or at **Fetch now**, posts once per video, and can be set to drafts, to wait for **Post**, or to leave Shorts out; a new site can be made in the same step. With a YouTube API key it brings the **whole back catalogue** and posts **premieres before they start**. It fetches only addresses it builds itself, from three YouTube hosts. *([guide →](YOUTUBE.md) · [ADR →](adr/ADR-0175-a-youtube-channel-as-a-blog.md))*

## 📧 A sovereign PGP mail server (VayuMail)

Your own mail server for your domain — **SMTP send and receive, IMAP and POP3**, **DKIM signing**, direct-to-MX delivery over STARTTLS, and **MX, SPF, DKIM and DMARC** records with live DNS checks — plus per-mailbox quotas, filters, an auto-responder, junk filing and full webmail. **PGP is native and automatic** (VayuPGP): every account gets a keypair, its private key encrypted at rest with AES-256-GCM, and its public key published by **Web Key Directory** so any client finds it. The composer speaks **PGP/MIME (RFC 3156)**, so an encrypted message carries attachments and several recipients. Subjects and names in any language read as written, everywhere. The Mailboxes page puts **new mail first**, across every domain, and follows it live. New mail raises a **bell and a desktop notification** — through the service worker, so it reaches the installed app too.

**A forgotten mailbox password is not a dead end.** There are five ways back in — single-use recovery codes, a verified recovery address off the install, a still-syncing trusted device, an administrator-approved one-time link, and a host break-glass command — all running one reset pipeline that revokes every app password and session, holds queued outbound mail, and writes an append-only audit trail. The public flow never confirms whether an address exists, and no reset mints a session or reveals a password. *([recovery guide →](MAIL-RECOVERY.md) · [ADR →](adr/ADR-0144-vayumail-account-recovery.md))*

**Mail that looks after you.** The reader says when a sender's domain **confirmed it sent the message**, sealed at delivery with a key only your install holds, blocks **tracker pictures** for good, strips tracking parameters from links, and warns when a link's words name one site and it goes to another. The Inbox **sorts itself** into Primary, Updates, Newsletters, Receipts and Social from headers alone, with Follow-ups and Unsubscribe views. Send a **sealed message to anyone**, opened with a passphrase on your own server; make a **throwaway address** for a website in a click and turn it off by every road; ask a model to **sum up or draft**, with its cautions enforced on the server. Signing in is guarded at every door: **failed attempts recorded and password spraying blocked by itself**, **passkeys** and passkey-only accounts, a **sign-in name** so the public address alone is useless, every open session listed, and a security checklist with a score. *([guide →](VAYUMAIL.md))*

## 📱 An official mobile app (VayuMail Mobile)

[**johalputt/VayuMail-Mobile**](https://github.com/johalputt/VayuMail-Mobile) — a pure-Go Android app that reads and sends your PGP mail and carries VayuTalk, from your own domain. You enter your domain and sign in once; the app **provisions its own per-device app password** (never your real password, revocable at any time) and configures IMAP and SMTP from VayuPress's own autoconfig endpoint. Encrypted mail is decrypted **on the device**. No tracking pixels, no remote content, no telemetry.

## 📅 A calendar, booking pages and files beside Mail (VayuMail Calendar and Drive)

**Calendar.** Every mailbox has a calendar beside its mail: a week, a month
or a day in the holder's own time zone, repeating events that keep their
local time, guests invited by mail with their answers shown, invitations
answered from the message they came in, and reminders on the holder's
devices and by mail. It is the calendar a phone and a computer already keep
in step over **CalDAV**, so an event made on either shows on the other; a
calendar can be exported and imported as `.ics`, and a feed at another
address can be subscribed to.

**Booking pages.** A link anyone can book a free time from: the lengths a
meeting can be, the holder's weekly hours, time kept free before and after,
how soon and how far ahead. The guest sees only free times, in their own
time zone, never what is in the calendar, and a time is booked only when
they confirm it by a link mailed to them.

**Drive.** A place for files in the same storage allowance as the mailbox's
mail: folders, upload by choosing or dropping files (large ones resumable),
a Trash kept for thirty days, the last five versions of a file, share links,
an attachment saved to Drive or attached from it, and **WebDAV**, so a
computer's or phone's own file app opens it. In a private mailbox a file is
sealed to the holder's key. *([Mail guide →](VAYUMAIL.md))*

## 💬 End-to-end-encrypted chat that lives only in memory (VayuTalk)

Real-time **PGP end-to-end-encrypted chat** for your domain, shared by the web console and the phone over one relay: a message typed on the web reaches the phone and back, indistinguishable to the server. Every message is encrypted to its recipient, held in a **bounded in-memory store that never touches disk**, and **destroyed the moment it is read**; an unread one is let go after 24 hours by default (anywhere from 5 minutes to 7 days, as the operator sets). **Safety numbers** shown identically on web and phone defeat a key swap. The relay serves on its own **`talk.yourdomain.com`**, past any CDN, so the live stream is never buffered or challenged. In the Tor world your chat identity is an **anonymous, rotatable code**, and opt-in, VayuTalk delivers **onion to onion** between two separate `.onion` installs. *([architecture →](adr/ADR-0131-vayutalk-ephemeral-messaging.md) · [over Tor →](VAYUTALK-ONION-FEDERATION.md))*

## 🛡️ A built-in bot shield (VayuShield "Aegis")

A self-learning bot shield in the same binary, in five layers that need **no operator command**: an **admin-sovereignty lane** that keeps the console answering while the public site is under application-layer load; a fixed-memory **probabilistic fair-shed** that catches spoofed botnets without touching a client inside its fair budget; a **reputation brain** that jails offenders in minutes and **forgives on its own**; silent-first **proof-of-work challenges** that calibrate themselves so real browsers are not bothered; and optional kernel-level **nftables and XDP offload**. **41 request-inspection rules** and published **datacenter ranges** (AWS, Google Cloud, DigitalOcean) feed a score that never blocks on its own. Search engines and AI assistants are always allowed; abuse is turned away with `429` or `503` and `Retry-After`, so search ranking and real readers are protected by construction. A **posture report** judges every control by what is actually enforcing, not by which switches are on. It watches the **sign-in doors** too: every failed sign-in recorded with the address kept masked, password spraying blocked by itself, and any source blockable from sign-in or from the whole site. *([architecture →](adr/ADR-0111-vayushield-bot-protection-and-analytics.md) · [sign-ins →](adr/ADR-0167-failed-sign-ins-recorded-and-stopped.md))*

**What it does not do, stated plainly:** it will not absorb a volumetric flood. Every layer above runs after a completed TCP handshake on your uplink, so a flood large enough to saturate that uplink is decided upstream of your server, by your provider's routers, and no software on the host can change that. VayuShield handles the abuse a publisher actually meets — scrapers, credential stuffing, comment spam, AI crawlers, application-layer floods. For volumetric resilience you need anycast capacity: a network in front, or several nodes of your own. The posture report keeps this as a permanent, visible fail rather than hiding it.

## 📊 Analytics with no personal data (VayuAnalytics)

Pageviews, sessions, top pages, referrers, UTM campaigns, custom events, funnels, retention, revenue and a live visitor panel — stored in your own SQLite. A visitor is a **server-side hash with a salt that rotates daily**, so visits cannot be linked across days: no cookies, no `localStorage`, no IP address or user agent stored, **no consent banner required**, and nothing to leak if the database is ever taken. Country comes from an **embedded offline table** — no GeoIP service, no phone-home — and a browser that sends Global Privacy Control is not counted. *([no personal data →](adr/ADR-0097-vayuanalytics-no-pii.md))*

## 🛠️ One console — VayuOS Still Air

Everything above is run from one console at `/os`, built in a design called **Still Air**: calm, precise and quiet, where the work is the loudest thing on the screen. **Home** opens on what needs you, the steps left to finish setting up, your visitors and the state of the install. Every page is built from the same few parts — hairline sections, rows with their control on the right, figures and disclosures — and colour appears only when a fact calls for it. The system bar shows the operating mode and the world (Clearnet or Tor) on every page, and **`⌘K` finds any page and runs any command**. Two schemes, Graphite and Paper, are drawn separately. TOTP two-factor, roles, a write-once audit log, and an install that reports its own mode — normal, degraded, read-only, recovery, maintenance or quarantined. It is **server-rendered Go and HTMX**, one stylesheet and one icon set from your origin, with no SPA framework and no build step. *([how the console is built →](ADMIN-UI.md))*

## 📲 Install VayuOS as an app

Open `/os` in any modern browser and it offers **Install VayuOS** — a one-click install onto your home screen or desktop as a standalone app; on iPhone and iPad, **Share → Add to Home Screen**. The installed app opens straight into the console and launches instantly — no store, no download. Its service worker **caches no console response**, so an installed VayuOS is never stale: every visit shows what the server serves now.

## 🌐 Many sites, one binary — VayuDomains and agency hosting

Point a second domain at the same server and it is **a site, not a copy of yours**: its own certificate, its own console, its own branding, content and analytics, and its own choice of blog, business template or hand-built bundle — changeable later without a migration. johal.in's install serves **fourteen hostnames across two apex domains from one process and one database**, every certificate renewing without a restart. There is no per-site process, config file or cron. *([agency hosting →](adr/ADR-0152-agency-hosting.md) · [domain independence →](adr/ADR-0153-domain-independence.md) · [one console per site →](adr/ADR-0154-one-console-per-site.md))*

## 🧅 A Tor onion for every domain (VayuTor)

Flip one switch and **every domain becomes reachable as its own Tor v3 `.onion`**, alongside its clearnet address, advertised to Tor Browser with the **`Onion-Location`** header. VayuPress runs **its own tor** as an unprivileged child over the control port (`ADD_ONION`), pinning each onion's key so the address is **stable across restarts** — no root, no `torrc`, no systemd — and it **fetches a current Tor itself** when the host's is missing or too old. It gets past **networks that block Tor** with an automatic ladder (direct, then 80/443, then **obfs4 bridges**, with the obfs4 transport built in); mints **vanity addresses** with a prefix you choose, without a key ever leaving the server; and sends **signed webhook alerts** when an onion drops or recovers. Its only metric is a count. *([onion services →](adr/ADR-0138-vayutor-onion-services.md))*

## 🌐🧅 Two worlds, one binary — Spaces

Run the install as **two separate worlds**. A **Clearnet Space** is your public HTTPS site, VayuMail and VayuTalk. A **Tor Space** is **web-only and anonymous**: a site on its own `.onion`, webmail-only mail, and anonymous chat. The Tor world is **built not to leak**: no clearnet callbacks (WKD, Gravatar, webmentions and MX are gated off), no hotlinked images, and http-onion serving with no CA certificate, no HSTS and no Secure cookie. One click moves the console into the Tor world and back. The two worlds keep **separate databases**, so accounts, mailboxes, keys and chat identities never cross; content moves only as a checksummed `vayupress migrate export|import` bundle. Both run on the same server, so this separates identity and content, not the machine — and the console says so. *([two worlds →](adr/ADR-0141-vayuos-spaces-clearnet-tor.md) · [anonymity model →](adr/ADR-0143-tor-space-anonymity-model.md))*

## 🔌 A fine-grained, scoped API (VayuAPI)

Drive the whole platform from code with **keys scoped to the exact section and action they need** — a **12 × 6 grid** of sections (posts, themes, design, plugins, domains, analytics, media, comments, members, mail, settings, backup) and actions (read, write, delete, apply, install, export), written `section:action`. A key gets an optional expiry and its **own rate budget**; it is stored **only as a hash**, shown once, and can be rotated, paused or revoked in a click. Every call is checked against the key's grant, metered (`429` and `Retry-After` when spent) and written to the audit log — so a script, a CI job or an AI agent can run your site without ever holding the keys to everything. *([reference →](compatibility/vayuapi.md) · [ADR →](adr/ADR-0134-vayuapi-fine-grained-keys.md))*

## 🤖 A native Claude / MCP connector (VayuMCP)

VayuPress serves its **own Model Context Protocol server** at `/mcp`, with **OAuth 2.1** discovery and client registration, so Claude (Desktop, Code, or the web) and any MCP client connect in one click. More than forty tools publish and edit posts, search content, apply themes, manage hosted sites and their files, read analytics and VayuShield's state, and more — each one granted only as far as the connecting key's scopes allow: give it the keys, or only `posts:write`. A tool the key does not grant is invisible and refused, and every call is rate-limited and audited. The agents in a **[Buzz](https://github.com/block/buzz)** workspace connect over the same endpoint. *([the connector →](compatibility/mcp.md) · [Buzz →](compatibility/buzz.md) · [ADR →](adr/ADR-0139-vayu-mcp-connector.md))*

## 🧩 An extension contract that is checked (VCB)

The **Vayu Compatibility Bible** turns "will this plugin or theme work?" into a checked contract. A developer — or an AI agent — writes a `plugin.json` or `theme.json`, runs **`vayu-compat`**, and knows before shipping that every hook, capability, colour, option, sandbox limit and CSP rule matches what the host enforces, because the validator **is the host's own code**. The live contract is at `GET /api/v1/vcb/contract`, and a running host validates a manifest at `POST /api/v1/vcb/validate`. *([the Bible →](compatibility/vcb.md) · [ADR →](adr/ADR-0135-vayu-compatibility-bible.md))*

## ⚙️ Automation that shows its work before it runs (VayuFlow)

Schedules, events and manual triggers drive **flows**: ordered steps over what the platform already does — content, mail, outbound calls, model calls — each **armed explicitly** before it can act. A flow can be **dry-run** to show the exact diff it would apply before anything is written. Runs carry idempotency keys and survive a crash, a durable inbox holds event triggers, and the whole engine is **inert in a Tor Space** rather than quietly reaching the clearnet. *([architecture →](adr/ADR-0151-vayuflow-automation-engine.md))*

## 🔭 Know what your endpoints are doing (VayuVeil)

An administrator's **observation console** for what this binary exposes: what is reachable, what is observed, and which units are switched on — read-only where reading is enough, with the controls that change something kept explicit and audited. *([architecture →](adr/ADR-0150-vayuveil-endpoint-observation-control.md))*

## 💳 Turn it on and get paid — Monetization

A complete, **redirect-based** monetization suite: no payment SDK is ever embedded, so your strict CSP stays untouched. Take payments through **your own Stripe and PayPal** — auto-renewing subscriptions and one-time purchases — **or crypto through your own BTCPay Server** (Bitcoin, Monero, Ethereum, stablecoins), settled by a verified webhook straight into *your* wallet with **no processor, no custody and no KYC**: the one rail an anonymous or Tor buyer can pay through. Sell **membership tiers** that unlock member-only posts, a **paywall** on any single post, **tiers that come with a real VayuMail mailbox** (with its quota, PGP key and chat), a **premium mail-address marketplace**, and **member ads** that wait in your moderation queue until you approve them. Every sale runs through one auditable **orders ledger**. *([subscription engine →](adr/ADR-0087-subscription-engine-v2.md))*
