# ADR-0152 — Agency hosting: per-domain sites, scoped client access, witnessed mail

- **Status:** Proposed
- **Date:** 2026-08-02
- **Relates to:** ADR-0132 (VayuDomains), ADR-0123 (privilege separation),
  ADR-0134 (API-key capabilities), ADR-0144 (mail recovery), ADR-0141 (Spaces)

## The claim, worded to be defensible

> **A web studio can run thirty client businesses on one modest server, where
> each client sees only their own site, mail and traffic; where no client can
> reach another client's data or the operator's controls; and where every
> statement made to a client about who can read their mail is one the code
> actually enforces.**

Note what that does *not* claim. It does not claim the clients are isolated from
each other at the process or kernel level — they are not; this is one binary.
It does not claim the operator cannot read a client's mail — §D4 explains at
length why that claim is not being made, and what is offered instead. It does
not claim the box survives a hardware failure. Those limits are named in §D5
because a business model built on a capability nobody has read the edges of is a
business model that meets those edges in front of a client.

## Context

### The business this exists to serve

A studio builds business websites. Today it hands the recurring revenue —
hosting, mailboxes, maintenance — to three other companies and keeps only the
build fee, while still taking the phone call when mail breaks. The published
position (`/vayupress-web-studio-hosting-business`) is that one binary on one
modest VPS can serve many independent client domains, each with its own site,
branded mail, certificate and statistics, at a marginal cost near zero.

That post is accurate about what ships today with one exception, corrected in
§D2: its onboarding checklist ends "give them a login", and the login it
describes cannot be given safely. Fixing the post is part of Phase 0.

### What already exists and is sound

ADR-0132 built the foundation across four stages, all shipped:

| Piece | State |
|---|---|
| `domains` registry (migration 059) | host, `site_type`, `mail_enabled`, `tls_state`, `config_json`, `status` |
| Content ownership (060) | `articles.domain_id`; pages are articles with `is_page=1`, so pages are owned too |
| Member attribution (061) | `members.domain_id` |
| Per-domain mail | isolated receive, read isolation, per-domain DKIM signing, autoconfig |
| Per-domain TLS + vhost | `scripts/setup-vayudomain.sh`, separate certificate lineage per host |
| `HasSecondaries` gate | a single-domain install pays nothing for any of it |
| `internal/customsite` | zip upload of a hand-built static site, zip-slip proof, extension allowlist, 50 MiB / 25 MiB / 3000-file caps, rollback |

The `customsite` package in particular is better than it needed to be, and it is
what makes the studio proposition credible: a site designed by hand, published
in one click, no build step on the server.

### What does not exist, established by reading the code

Four findings, each verified against the source rather than inferred.

**1. Websites are install-wide, not per-domain.** `customSiteDir()` returns
`filepath.Join(filepath.Dir(config.Cfg.MediaDir), "custom-site")` — no domain
component — and `bizSettings` reads mode, template and content from global
settings keys. So one custom bundle serves every domain on the install. A studio
can host *a* hand-built site; it cannot host thirty different ones. **This, not
the Content-Security-Policy, is what blocks the product.**

**2. Roles are install-wide.** The `users` table (migration 020) carries no
domain column, `Authenticate` keys on a globally unique email, and the login form
is served on every host. Session cookies are host-only (no `Domain` attribute),
but the token is domain-agnostic server-side: `Validate()` takes no host and
`requireSessionOrAPIKey` never reads `r.Host`. Host-only cookies are a browser
convention here, not a boundary. So today the only logins available to give a
client are `author`/`editor` (unscoped) or `admin` (owns the server).

**3. Authorization defects.** An adversarial pass over the design raised four.
Verifying each against the source before acting on it confirmed two as stated,
found one overstated and one wrong — recorded here in corrected form, because an
ADR that repeats an unverified finding is the same defect as a panel that
overstates what is enforcing.

- **Confirmed, and serious.** A scoped `mail:write` API key can `POST
  /os/vayumail/accounts/create` with `role:"administrator"`. The prefix
  `/os/vayumail` maps to `SectionMail`, so the key passes `keyMayCall`;
  `isAdminRequest` then returns `auth.HasValidAPIKey(r)` — *any* valid key, not a
  superuser key — so the handler's admin gate is satisfied;
  `mailConsoleAccess(RoleAdministrator)` maps the new mailbox to
  `users.RoleAdmin` with console access. Signing in with that credential yields
  full console administration. `accounts/update` accepts a `Role` field and
  promotes an existing mailbox the same way.
- **Confirmed.** `QueueRetentionDays` has no entry in `DefaultConfig`, so it took
  Go's zero value, which means keep forever. `vayumail_queue.raw` is the full
  RFC5322 message, so the delivery queue was a permanent plaintext archive of
  everything the install had ever sent, inside SQLite and therefore inside every
  database backup.
- **Overstated.** `/os/api/vayuos/health` was reported as having "no
  authorization at all". It is in fact behind `requireSessionOrAPIKey`. The real
  defect is narrower: it sits at author level *and* `mailOnlyPathAllowed`
  admitted the whole `/os/api/vayuos` prefix, so a mail-confined principal — a
  reader who claimed a mailbox, not staff — could read the operator's
  component-by-component health snapshot including detail strings. An agency
  client would have inherited exactly that.
- **Wrong as reported.** `POST /api/v1/analytics/collect` was said to trust the
  `Host` header. It never reads `r.Host`. The finding is a *design constraint for
  the traffic phase* — when per-domain attribution is added, it must not be
  derived from anything the client controls — plus a pre-existing integrity
  weakness: the beacon is unauthenticated and rate-limited per IP, so arbitrary
  paths can be submitted. Neither is a live cross-tenant leak.

**4. Nothing makes stored mail unreadable by the operator.** Messages are
plaintext RFC5322 files on disk. Every PGP private key is server-held and
server-openable. An admin reads any mailbox through webmail in two clicks.
`applyMailPasswordReset` resets any hash from the console or the CLI, and
`DisableTOTP` clears a client's second factor in one POST with no code and no
notice. A password change plus two-factor authentication therefore delivers
*nothing* against the operator, and any product built on the belief that it does
would be selling a false claim.

Also relevant, and deferred rather than solved: `analytics_daily` is
`PRIMARY KEY(day,path)` with no domain dimension, so two client domains sharing
`/about` have **merged** view counts; and media is a flat directory served as
`/media/<name>` with no ownership concept.

## Decision

### D1 — Websites become per-domain

`customSiteDir()` takes a domain id and resolves to `…/custom-site/<domain_id>/`.
The zip-slip confinement, extension allowlist, size caps and rollback are
untouched; only the base path moves. Site mode, business template and business
content move from global settings keys into `domains.config_json`, which
migration 059 already created as the per-domain override blob.

The per-bundle cap becomes a per-domain figure recorded beside the allowance in
§D3, so the storage a studio grants a client is one number covering their site
and their mail.

**The Content-Security-Policy does not change, and does not need to.** The
baseline from `securityHeadersMiddleware` is
`script-src 'self' 'nonce-…'`, `style-src 'self'`, `style-src-attr
'unsafe-inline'`, `font-src 'self'`, `img-src 'self' data: https:`. A
self-contained bundle satisfies it: external stylesheet and script *files* are
`'self'`; inline `style="…"` attributes are already permitted; images may come
from any https origin; fonts ship in the bundle (`.woff2` is on the allowlist).
What a bundle may not contain is an inline `<script>`, an inline `<style>` block,
or a reference to a third-party CDN.

That is a packaging constraint on the site, not a limitation of the platform, and
it makes the result better: no third-party requests means fewer round-trips and
no hotlinked font service, which is a defensible line in a proposal rather than a
compromise.

**Isolation between clients is the browser's, not ours.** Each client sits on its
own origin, which is the strongest boundary available on the web. An iframe
sandbox inside that would be a weaker boundary within a stronger one, and would
cost search visibility and deep links; `frame-ancestors 'none'` forbids it in any
case. Where a single client genuinely needs a third-party origin, the extension
is per-domain, per-mode, and drawn from a **closed allowlist** — the pattern
`render.BuildCSP` already uses for video `frame-src`, where callers pass origins
and anything not in the map is dropped. Two rules are absolute: never
`'unsafe-inline'` or `'unsafe-eval'` in `script-src`, and never inject nonces
into uploaded HTML, which is `'unsafe-inline'` under another name.

`assertCSPSafe` is a test helper over the admin console's own markup. It has
never governed uploaded bundles and is not extended to them.

### D2 — The client role, and a gate whose zero value denies

```sql
ALTER TABLE users ADD COLUMN client_domain_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_users_client_domain ON users(client_domain_id);
```

Additive, default `''`, so every existing install is byte-identical. A client is
never bound to the primary domain — that is the studio's own install — so
`role='client'` with `client_domain_id=''` is an invalid identity and is refused
at every request rather than defaulting to anything.

A new role `client` joins the ladder but is not a rung on it. The existing ladder
is linear and cannot express "may see the Website page but not Posts". A client
reaches a fixed, enumerated surface and nothing else.

The enforcement is the important part. The current default for non-API `/os`
pages is permissive: a path matching no admin or editor area is reachable by an
author. For a user the studio does not control, that default is a breach waiting
for the next feature. So audience becomes a declared property whose zero value is
a refusal:

```go
type osAudience uint8

const (
    audienceUnset    osAudience = iota // zero value — never valid
    audienceOperator                   // operator / staff only
    audienceClient                     // also reachable by a bound client session
)
```

Two consequences carry the design. A page added next year that declares nothing
is denied to clients automatically, so forgetting is safe. And a CI test walks
the route table and **fails the build** if any `/os` route has no declared
audience, so forgetting is caught anyway. This mirrors the architecture-layer
validator already in the pipeline.

Nav filtering is not a control. A client hitting an undeclared URL directly is
refused with a 403 for API paths and a redirect for pages; the surface table is
the single source of truth for both routing and navigation, so what is shown
cannot drift from what is reachable.

Three doors must close, not one. The API-key path bypasses session role checks
entirely and carries no domain dimension; the member-plus-mailbox fallback is a
second entry; the MCP tools enumerate library-wide. A client holds none of them.

Clients log in at the primary domain. Serving login on client hosts would present
a boundary the server does not enforce, given the domain-agnostic token; the
boundary is the row, resolved on every request.

The client's surface is exactly three areas — **Mail** (their own mailbox),
**Website** (their brand and site facts, not the post editor) and **Traffic**
(their own host). No blog, no editor, no media library, no dashboard, no
infrastructure, no API keys, no mailbox creation.

Traffic ships only after `analytics_daily` gains a domain dimension and the
ingest path stops trusting the `Host` header. Showing a client a merged number
with a caveat attached would be a cross-tenant leak with a footnote, which is
worse than showing nothing.

### D3 — Mailboxes are minted by the operator, and refused to everyone else

Mailbox creation requires a real admin session, never an API key, and is bounded
by a per-domain allowance the operator raises on request. The client's page
carries a request box, not a create button — and the refusal lives in the handler,
because absence from a menu has never stopped anyone typing a URL.

The allowance and the storage grant live beside each other in `config_json`, and
the client's own brand save must not be able to overwrite them: a save that
serialises the whole blob would let a client raise their own limit by editing
their site colours.

### D4 — Mail privacy: witnessed access, and why not encryption

The requirement was that after handover the operator cannot read the client's
mail. Password changes and second factors do not deliver it, per finding 4. Only
encryption under a key the server does not hold would.

**Cryptographic sealing is designed and deliberately not built.** The mechanism
is sound — a content key wrapped under a passphrase-derived key, encryption at
the delivery hook, the server-held private half purged. It loses on the business,
not the cryptography:

- **Custody.** The first thing a non-technical client says is "can you set it up
  on my phone for me?" Complying means holding the passphrase. The panel keeps
  rendering *sealed* and every test keeps passing while the claim is false.
- **Loss rate.** At sixty clients, the annual probability that a small business
  loses both a passphrase it never types and a sheet of paper is not small. Over
  three years that is double-digit unrecoverable mailboxes, each destroying
  business records, each blamed on the studio. One such story ends a
  referral-driven practice.
- **Missing prerequisites.** There is no mail export path in the codebase, so
  "you own your mail" would be false on the only day it matters. There is no
  self-service importer for the years of history every real client arrives with.
  The studio's own mobile app fetches the armored private key from the server and
  would break on already-installed handsets.
- **Shared addresses.** A role mailbox such as `sales@` sealed to one employee
  becomes company property held hostage when that employee leaves.
- **Support economics.** Roughly a quarter of the total support budget for a
  plan priced for near-zero touch, spent on one optional feature.
- **It may lower real confidentiality.** After sealing, the operative key is a
  printed sheet in an unlocked drawer. That is not obviously safer than a vetted,
  contractually bound operator.

**What ships instead is witnessed access.** A one-way per-mailbox handover state
severs every route by which the operator reaches the mailbox through the product:
the panel read path; the studio's own console password over IMAP; password reset;
second-factor disable; and app-password minting. That last severance is the one
that matters most — without it an operator mints a credential and reads
everything over IMAP with no record, quieter and cheaper than any loud path.
Forwarding, filter rules and the autoresponder are refused at the store level for
a handed-over mailbox, because retransmission is the cheapest quiet exfiltration
channel.

What remains is a command-line break-glass that cannot run without committing an
append-only, hash-chained, client-visible ledger entry and a notice to a contact
outside the install.

The read authority becomes a required argument rather than a check to remember.
The decision is currently duplicated across a helper and a dozen inline copies,
including one attachment handler that consults none of them — so fixing one and
missing another leaves a working operator read path with a green suite:

```go
type readerKind uint8

const (
    readerUnset readerKind = iota // zero value — no authority
    readerOwner
    readerOperator
    readerSystem
)
```

A zero `Reader` returns an error. Every read entry point takes one, and
`ReadAsOperator` is minted in exactly one place.

**The sentence the studio may put in front of a client, and no stronger:**

> After we hand your mailbox over, our staff can no longer open it from the admin
> panel, our own admin password stops working on your account, we cannot reset
> your password or turn off your second factor, and we cannot create a mail
> credential for your mailbox — the only way left in is an emergency override
> that will not run without first writing a permanent entry into the access log
> you can see. **Your mail is not encrypted.** It is stored as readable files on
> a server we operate, so anyone with direct access to that server, its database,
> or a backup could still read it — only encryption under a key we do not hold
> would prevent that, and we do not offer it today.

### D5 — What is deliberately not isolated

- **One process.** A remote-code-execution bug reaches every client at once. Row
  scoping is not a sandbox.
- **One box.** A hardware failure takes every client down together. Off-box
  encrypted backups with restore drills are mandatory, not optional.
- **Shared DKIM key.** Deliverability is per-domain and correct; the key is one
  thing.
- **The operator runs the binary.** Every promise here is kept by software the
  studio installs and updates.
- **Mail metadata.** Sender, recipient, subject, size and delivery logs are
  visible regardless of handover.
- **Mail routing.** The operator owns MX and address creation, so future mail can
  be redirected even where the archive cannot be opened.

## Phasing

Each phase lands on `main` as its own commits under `## [Unreleased]`. One
release is cut at the end of the whole track, after an adversarial pass over
everything accumulated.

| Phase | Contents | Size | What it unlocks |
|---|---|---|---|
| **0 — Safety** (shipped) | The three real defects in finding 3: console-capable roles minted only by a session; a queue retention default; the mail-only prefix narrowed. The analytics item moves to Phase 4, where it is a design constraint rather than a live hole. Post correction outstanding | ~120 lines | Nothing directly; closes a full privilege escalation. Shipped independently of everything below |
| **1 — Per-domain sites** | `customSiteDir(domainID)`, mode and content into `config_json`, per-domain cap | ~1–2 days | Selling any design the studio can build |
| **2 — Client role** | Migration 079, `RoleClient`, the audience table, Mail and Website pages | ~700 lines | The client login. The sellable unit |
| **3 — Provisioning** | Operator-only mint, allowance, request box | ~300 lines | Metered mailboxes |
| **4 — Traffic** | Migration 080, ingest fix, per-domain view | ~400 lines | The third pillar of the pitch |
| **5 — Handover** | Handover state, ledger, the five severances, break-glass | ~1,200 lines | The privacy sentence in §D4 |
| **6 — Sealing** | Not scheduled | — | Only with export, importer, mobile support and a two-holder rule first |

Phase 1 precedes Phase 2 deliberately. Per-domain sites are what make the product
sellable at all; the client login is what makes it scale without consuming the
operator's time. The reverse order yields a restricted panel for a website the
studio can only host one of.

## The adversarial pass this design already survived

Thirty-eight findings, six critical and twenty-one high, across three lenses: a
malicious tenant, a reviewer hired by the client to break the privacy claim, and
three years of operating a one-person studio. The findings that changed the
design rather than confirming it:

- An operator minting an app password read a handed-over mailbox over IMAP with
  no record — the earlier claim was false. Severance five exists because of it.
- Signing in with a mailbox credential escaped the client class entirely, because
  the existing mail-only class is *wider* than the client surface, not narrower.
- A client saving brand colours wiped the operator-set mailbox allowance, both
  living in the same blob.
- The outbound queue is a permanent plaintext archive of everything a client
  sends. Disclosed in §D4's claim rather than hidden.
- Six critical findings against cryptographic sealing, none of them about
  cryptography. §D4 records the decision they produced.

## The test suite

In the attacker's voice, with the consequence in the name, and every one
mutation-tested — re-break the fix and confirm the test goes red. A finding with
no test is an opinion; a test that passes against the broken version proves
nothing.

- A client requesting each operator-only path is refused; adding a bare `/os`
  prefix to the surface table must fail the suite.
- Every `/os` route declares an audience; registering one that does not must fail
  the build, naming it.
- A client whose binding is empty, disabled, or the primary domain gets no
  access.
- Every mail route, passed another client's address as a parameter, returns the
  caller's own mailbox — never the victim's, and never a bare refusal that hides
  the substitution.
- A brand save leaves the operator-set allowance unchanged.
- Analytics ingest refuses a forged `Host`.
- A `mail:write` key cannot mint an administrator mailbox; the resulting
  credential cannot log into the console.
- A mailbox credential does not escape the client class.
- After handover: password reset, second-factor disable, app-password minting and
  panel reads all refuse, and break-glass aborts if the ledger append fails.

## Consequences

**Good.** The studio sells a website it designed, not a template; a client login
that cannot reach anything technical; and a privacy statement that survives a
hostile reading. Single-domain installs remain byte-identical throughout, because
every migration is additive and every new surface is gated.

**Costly.** Six phases before the whole track releases. Traffic depends on a
table rebuild. The privacy feature the owner originally asked for is explicitly
not shipped, and the weaker sentence must be used in sales material until the
prerequisites in §D4 exist.

**Accepted risks.** One process and one box, named in §D5 and priced into the
proposition rather than hidden. Media has no ownership model and is therefore
absent from the client surface entirely; a filtered listing over an unfiltered
public serve path would be a claim the code does not honour, so no such listing
is offered.

## Open decisions

| # | Decision | Recommendation |
|---|---|---|
| 1 | Sealing or witnessed access | **Witnessed access**, with the §D4 sentence. Revisit when a client asks unprompted and will pay separately |
| 2 | Where clients log in | **Primary domain.** A per-host login page would imply a boundary the server does not enforce |
| 3 | Traffic in the first release | **Only after** the domain dimension and the ingest fix. Never a merged number with a caveat |
| 4 | Client post and page editing | **Not until** `articles` has a per-record ownership check on save and delete |
| 5 | Media ownership | **Out of scope**, and therefore out of the client surface |
| 6 | Per-domain DKIM keys | **Out of scope**, disclosed in §D5 |
| 7 | Continuity plan | **Written — `docs/CONTINUITY.md`.** It was supposed to precede Phase 5 and did not: the privacy feature this ADR judged less valuable shipped first, and the plan followed it. Recorded rather than tidied away, because the ordering was the decision being got wrong |
