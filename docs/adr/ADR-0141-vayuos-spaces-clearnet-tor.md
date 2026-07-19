# ADR-0141 — VayuOS Spaces: Clearnet and Tor as separate, independently controllable worlds

Status: Accepted (Phase 1 in progress — scheme-aware public origin shipped)
Date: 2026-07-19
Deciders: VayuPress core

## Context

VayuTor (ADR-0138) gives every hosted **clearnet** domain a paired v3 `.onion`,
"clearnet + onion together", and **deliberately refused** a Tor-only switch —
onion was always minted from, and routed back to, a clearnet domain record. That
is excellent for censorship-resilience of an existing public site, but it means:

1. There is **no way to run a site that has no clearnet domain at all** (fully
   anonymous / Tor-only). The onion set is derived from `StatusActive` clearnet
   hosts and explicitly filters out `.onion`; the primary domain must be a real
   clearnet FQDN and cannot be removed.
2. Clearnet and Tor concerns are **meshed** in one admin surface. An operator
   who wants to run and control the two worlds separately (turn one off, keep the
   other, keep anonymous content away from public content) cannot.
3. Every absolute URL (canonical, OG, JSON-LD, sitemap, RSS, robots) is hardcoded
   `https://<clearnet domain>` because the onion request's Host is rewritten back
   to the clearnet host **before** rendering — so a Tor-only site would emit dead
   `https://` links.

The operator's requirement: **two clear, first-class options in VayuOS —
Clearnet 🌐 and Tor 🧅 — that work simultaneously, can each be switched off
independently, and are not mashed together.** Plus a **Tor-native VayuMail**
(onion-to-onion mail) and a **VayuTalk anonymous identity** that does not require
a mailbox. Both "powers" available at once; each site/identity may be **Synced**
(one identity, reachable both ways) or **Separate** (an independent, anonymous
island).

## Decision

Introduce **VayuOS Spaces**: the admin console and the platform are organised
into two isolated, independently-controllable worlds.

- **🌐 Clearnet Space** — public domains, global VayuMail (SMTP/IMAP, MX/DKIM,
  CA-TLS), mail-linked VayuTalk, normal analytics. This is today's behaviour.
- **🧅 Tor Space** — onion services (may exist with **no** clearnet domain),
  **VayuMail·Tor** (onion-to-onion / instance-local only, no MX/DNS/CA-TLS),
  anonymous rotatable VayuTalk identities, count-only stats, and **enforced
  anti-leak guardrails**.

A persistent **space switcher at the top of VayuOS** flips between them; each
Space is scoped, themed distinctly (so the operator always knows which world they
are in), has its own master on/off, and its data never meshes with the other.

Reversing ADR-0138's Tor-only refusal is an explicit, guard-railed decision made
here: onion-only is now a supported per-site mode, never a foot-gun (the app
always stays reachable on `127.0.0.1`, and suppressing clearnet requires explicit
confirmation).

### Core technical model

1. **Reach is two independent per-site switches** (`clearnet on/off`, `tor
   on/off`). Both on = co-hosted (today). Tor-only = anonymous. Clearnet-only =
   classic. A site is keyed in the existing domains registry; a `site_type`
   discriminator (`clearnet` | `onion`) plus onion-primary support lets a site
   exist keyed by its `.onion` with no clearnet host.

2. **Identity is Synced or Separate per site.** Synced = the onion is a second
   door to the same site/accounts (correlatable, for availability). Separate = an
   independent onion-only site/mailboxes/Talk IDs (uncorrelated, for anonymity).

3. **Scheme is request/host-derived, not global.** A single source of truth,
   `seo.Origin(host)`, returns `http://` for a `.onion` host and `https://`
   otherwise. All absolute-URL generation flows through it. The onion middleware
   stops rewriting an onion-primary Host back to clearnet, so renderers can be
   scheme-aware per request. (Phase 1, shipping incrementally — the `seo.Origin`
   backbone lands first, byte-identical for clearnet, guarded by the compat
   golden tests.)

4. **`Secure` cookies become request-aware**, off for onion requests (Tor
   Browser treats `.onion` as a secure context, but plain-http onions must not
   force `Secure`) while staying on for any clearnet host — never a global
   disable.

5. **Anti-leak is enforced, not advisory, in the Tor Space.** External
   `http(s)://` images/assets, gravatar/WKD/MX/IndexNow/webmention clearnet
   callbacks, and any clearnet `fetch()` are blocked so a Tor Space page cannot
   de-anonymise its readers. `img-src 'self' data:` stays tight (never widened to
   `http:`). A dead `https://<onion>` link is a correctness bug; a live external
   asset is a **blocking** de-anonymisation bug.

6. **VayuMail·Tor** is a separate service in the Tor Space: instance-local
   same-onion delivery needs zero DNS/TLS (works today); optional onion-to-onion
   federation dials the peer's mail onion over Tor SOCKS with key discovery over
   the onion. Global email interop (Gmail/Outlook) is **out of scope by physics**
   — it needs a public MX/PTR/DKIM that pins the server to a clearnet IP — and is
   only ever available as an explicit, clearly-labelled non-anonymous opt-in in
   the Clearnet Space.

7. **VayuTalk anonymous identity** is a standalone, rotatable keypair addressed
   by fingerprint / copy-paste share string (`talk:<onion>/<fingerprint>`), no
   mailbox. The relay is already identity-agnostic, so it carries both mail-linked
   and anonymous identities at once; the operator/user picks per conversation.

## Consequences

- **Positive:** true Tor-only anonymous hosting becomes possible; the two worlds
  are cleanly separated and independently controllable; onion sites emit correct
  URLs; the anti-leak guardrails make "anonymous" mean it. Most of the machinery
  (VayuTor engine, domains registry, local mail delivery, identity-agnostic Talk
  relay) is reused, not rebuilt.
- **Negative / limits:** global email cannot be anonymous (documented, gated);
  Synced identity is correlatable by design (documented); losing nginx in a
  Tor-only deploy moves rate-limiting into the Go layer.
- **Reversal of ADR-0138:** the "no Tor-only switch" decision is superseded here,
  with explicit lock-out and confirmation guardrails.

## Phased delivery

- **Phase 1 — Spaces framework + onion-only site/blog + admin.** Scheme-aware
  `seo.Origin` (shipped), then: `site_type`/onion-primary, stop the onion→clearnet
  Host rewrite for onion-primary sites, request-aware `Secure` cookies, CORS +
  callback no-ops, onion-only deploy branch, Go-layer rate limiting, the top-bar
  space switcher + per-Space scoping/theming, CI gate (no `https://` / no external
  asset in onion artifacts). **This ADR.**
- **Phase 2 — VayuMail·Tor** (instance-local → onion-federated; separate Tor
  inbox; clearnet-send block). Future ADR.
- **Phase 3 — standalone rotatable VayuTalk identity** in the Tor Space. Future
  ADR (amends ADR-0131).

## Related

- ADR-0138 (VayuTor onion services) — superseded on the Tor-only decision.
- ADR-0134 (VayuAPI) / ADR-0139 (VayuMCP) — endpoints ride the same mux, so they
  work in either Space; the OAuth consent `form-action` must include the onion
  origin when served over Tor.

## Amendment — 2026-07-19: finalized operating decisions

After review, the model is refined from "per-site Space" to a **whole-install
mode switch**, and the Tor world is **web-first**. These are the decisions we
build to:

1. **Whole-install mode, not per-site.** A single flag `VAYUOS_MODE = clearnet |
   tor` selects the entire instance's world. To run both, the operator runs **two
   separate VayuPress installs** — a Clearnet install (nginx + certbot + public
   DNS + mobile apps) and a Tor install (http onion, no certbot, anti-leak
   enforced) — each with its **own database**. That physical separation *is* the
   isolation and the strongest anonymity boundary (no shared identity, no
   correlation, either can be switched off by simply not running it).

2. **Tor clients are web-only (via Tor Browser).** A mobile app is a large
   de-anonymization surface (needs Orbot/embedded Tor, leaks are easy), so the
   Tor install is accessed through **Tor Browser**:
   - **VayuMail·Tor = webmail only.** No mobile-app support for the Tor install;
     the mobile apps stay on the Clearnet install. (This also removes the mobile
     SOCKS-dialer work.)
   - **VayuTalk over Tor = web client only.** A mobile-over-Tor Talk client is
     deferred and only ever considered if it can be proven not to leak; until
     then, anonymous Talk lives in the browser over Tor Browser.

3. **Sync & Migrate — content only, two mechanisms, with a hard warning.** Data
   moves between the two separate installs by an explicit tool; **identities never
   cross**:
   - **Scope = content only:** posts, pages, media, themes, menus, settings,
     redirects. **Never** accounts, mailboxes, PGP private keys, sessions, or Talk
     identities — syncing those would de-anonymize the Tor install. Identity
     export is not offered.
   - **Mechanism A — Portable Bundle (primary):** a signed, checksummed
     `.vaybundle` export you move manually (works fully offline / by USB — the
     safe path for an air-gapped anonymous box). Import supports **Merge** (upsert
     by slug) or **Replace**; media is content-addressed so re-import is
     idempotent. Serves both one-time **migrate** and periodic **mirror** (by
     re-importing).
   - **Mechanism B — Live Mirror agent (opt-in):** a scheduled pull of content
     from the source install's API over Tor SOCKS (typically clearnet→onion for a
     censorship mirror). **Off by default**, and enabling it shows a blunt warning:
     *a live link between the two installs makes them correlatable and reduces the
     anonymity of the Tor install.*
   - **Anonymity note reiterated:** any sync makes the two installs correlatable
     by content; sync is an availability/convenience feature, not an anonymity
     one. A truly anonymous Tor install keeps its own content and never syncs.

### Revised phased delivery

- **Phase 1 — whole-install mode + onion-only site/blog + admin.** `VAYUOS_MODE`
  flag, onion-primary identity, stop the onion→clearnet Host rewrite, scheme-aware
  origin (shipped), request-aware `Secure` cookies, CORS/callback no-ops, onion
  deploy branch, Go-layer rate limiting, anti-leak enforcement, and the VayuOS
  top-bar mode indicator.
- **Phase 2 — Content Bundle export/import** (content-only, Merge/Replace, signed)
  plus the opt-in Live Mirror agent with warning.
- **Phase 3 — VayuMail·Tor (webmail only)**: instance-local → onion-federated,
  no mobile.
- **Phase 4 — VayuTalk anonymous web client** over Tor (rotatable ID); mobile
  deferred pending a leak-proof design.

### Phase 1 — implementation status (shipping incrementally)

Delivered so far:

- `seo.Origin(host)` scheme backbone — `.onion` → `http://`, clearnet → `https://`
  (v3.14.2).
- Whole-install `VAYUOS_MODE` switch → `config.Cfg.OnionMode`
  (`clearnet` default; `tor`/`onion`/`anonymous` enable Tor mode) (v3.14.4).
- Anti-leak callback gates in Tor mode — IndexNow, social auto-post, Cloudflare
  purge no-op; browser-enforced `img-src 'self' data:` CSP (v3.14.4–v3.14.6).
- Onion-primary routing — in Tor mode the `.onion` Host is preserved to the domain
  resolver instead of being rewritten to a clearnet host (v3.14.7).
- VayuOS top-bar Space-mode indicator (Clearnet/Tor badge) (v3.14.8).
- **One-command Tor Space provisioner** `scripts/setup-tor-space.sh` — stands up a
  SECOND `vayupress-tor` instance (own DB, own persistent `.onion`,
  `VAYUOS_MODE=tor`) alongside an existing clearnet install, so both worlds run at
  once with nothing shared (v3.14.9).

Still open in Phase 1: a from-scratch onion-only branch inside
`deploy-vayupress.sh` (skip the certbot/HTTPS path when `VAYUOS_MODE=tor`), a
VayuOS "Spaces" admin page surfacing the provisioner + the live `.onion`, and
request-aware `Secure` cookies (largely moot — Tor Browser treats `.onion` as a
secure context).

### Amendment — Anonymous Tor Space via a self-supervised child (no root, no terminal)

The sibling-service model (`scripts/setup-tor-space.sh`) is the hardened form,
but it needs root/systemd. To give a **one-click, in-VayuOS** Anonymous Tor Space
with **no server-side work**, the running clearnet process instead **supervises a
second copy of itself** as the Tor world — reusing patterns the binary already
uses unprivileged: it self-execs its own binary (as it does for the built-in
obfs4 transport), supervises a child process (as managed-Tor does), and mints the
onion over the already-running managed-Tor control port (`ADD_ONION`, whose
target is an arbitrary local port). No root, no systemd, no `torrc` edit.

Isolation contract (`internal/vayuos/torspace`): the child runs with a **curated
environment** — never the parent's (which would leak the API key and correlate
the worlds). It gets a **distinct API key** and every writable path
(`DB_PATH`/`CACHE_DIR`/`MEDIA_DIR`/`STATIC_DIR`/`LOG_DIR`/`TMP_DIR`) pinned under
`DATA_DIR/tor-space`, so it inherits the parent unit's `ReadWritePaths` with no
new grant and no content/media bleed. A `VAYUOS_SPACE_CHILD=1` sentinel is the
recursion guard (the child never supervises a grandchild or runs an onion engine).

**Honesty — what this is and is not.** This provides content/identity
**compartmentalisation**: separate database, media, accounts, PGP keys, Talk IDs
and a separate stable `.onion`, plus the Tor-mode anti-leak posture. It is **not**
anonymity against an adversary with server access or network-level correlation:
both worlds share the same host, UID, disk and Tor daemon, and the clearnet
install is already an identified entity — a server seizure, disk image, or
coincident uptime links them. Anonymity that survives that requires a physically
separate machine (the sibling-service install). The admin UI must state this
plainly and never market the child mode as unlinkable anonymity.

Ship-gating musts before the child is spawned by a live toggle (enforced across
the increments): curated secret-excluding env with a distinct API key (done,
`torspace` package); exponential crash-loop backoff + breaker; reap by an exact
role marker (never by binary name — parent and child share it); a verified
graceful SQLite drain (SIGTERM → checkpoint → SIGKILL); robust child-port
capture; the dedicated onion's reserved host exempt from the reap loop; and the
parent backup sweep excluding `/tor-space`.
