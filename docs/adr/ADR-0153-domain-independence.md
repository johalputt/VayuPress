# ADR-0153 — Every domain a whole install

**Status:** Proposed — awaiting decision on §Open decisions
**Supersedes the scoping half of:** ADR-0152 (agency hosting), ADR-0132 (per-domain content)

## The claim this record retracts

`/os/domains/{id}` tells the operator, on the "Design & more" card:

> Deeper editing lives in the shared tools — they apply per domain once this
> site is selected.

**That sentence is false.** Theme Studio, Website settings, SEO and Analytics do
not scope to the selected domain. They read and write one install-wide store.
The sentence was written against tools whose scoping was assumed rather than
checked, and it is the reason the panel looks like it offers independence it has
never had.

This ADR exists because an operator followed that sentence, found the tools
linked, and was right.

## What is actually scoped today

Measured, not recalled.

| Surface | Storage | Per-domain? |
|---|---|---|
| Posts / pages | `articles.domain_id` (060) | **Yes** |
| Members | `members.domain_id` (061) | **Yes** |
| Client login binding | `users.client_domain_id` (079) | **Yes** |
| Daily view counters | `analytics_daily.domain_id` (080) | **Yes** |
| Referrer counters | `analytics_referrers.domain_id` (080) | **Yes** |
| Site name, tagline, description, 3 colours | `domains.config_json` | **Yes — 6 fields** |
| Site mode / template / bundle | `domains.config_json` | **Yes** |
| Mailbox allowance | `domains.config_json` | **Yes** |
| **Everything else — 327 setting keys** | `site_settings(key PRIMARY KEY, value)` | **No** |
| Theme (colours, custom CSS, typography) | `site_settings` | **No** |
| SEO defaults, head meta, social cards | `site_settings` | **No** |
| Analytics event log, top pages, trending | `analytics_pageviews` — **no domain column at all** | **No** |
| Newsletter, comments, monetization, integrations | `site_settings` | **No** |
| Media library | shared filesystem | **No** |

The shape of the defect is one line of schema:

```sql
CREATE TABLE site_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '');
```

There is no scope column, so there is no scope. Every per-domain behaviour built
so far is a **bespoke overlay** bolted beside that table, and the overlay is six
fields wide:

```go
// middleware_domain.go — the entire per-domain override surface
s := render.GetActiveSettings()   // the ONE global settings object
applyBrand(&s, b)                 // overlays name, tagline, description, 3 colours
```

So a secondary domain is not an independent site. It is **the primary site
wearing a different name and three colours**, and every one of the other 321
keys is the operator's own blog leaking into the client's.

### Why this keeps happening

`render.GetActiveSettings()` is an **ambient global read**. It takes no scope, so
it cannot be wrong at the call site, so nothing forces a developer adding a
feature to think about which domain they are serving. Sixty-odd call sites
inherited the primary's configuration by *default*, silently, and each new
feature inherits it too.

That is the same failure mode this codebase has now hit four times: a decision
that can be omitted will be omitted, and the omission looks like working code.

## Decisions

### D1 — Scope becomes a required argument, and its zero value is invalid

The spine of the whole design, and the only part that makes the rest hold.

```go
// settings.Scope — who these settings belong to.
type Scope struct{ kind scopeKind; domainID string }

const (
    scopeUnset  scopeKind = iota // zero value — never valid
    scopePrimary                 // the operator's own install
    scopeDomain                  // one hosted domain
)

func ForPrimary() Scope
func ForDomain(id string) Scope        // refuses "" — that is the primary's sentinel
func ScopeFromRequest(r *http.Request) (Scope, bool)

func (s *Store) Get(ctx context.Context, sc Scope, key string) string
func (s *Store) Set(ctx context.Context, sc Scope, key, val string) error
```

An unset `Scope` returns an error on write and the compiled-in default on read —
never the primary's value. The compiler finds all ~60 call sites; a future one
cannot silently inherit, because there is nothing to inherit from.

`GetActiveSettings()` is **deleted**, not deprecated. A global read that still
compiles is one a hurried change will reach for.

### D2 — Isolation by default, with an explicit copy

The question that decides whether this feels independent: when a domain has no
value for `theme.accent_light`, what does it get?

- **Inheritance from the primary** — convenient, and *exactly what produces the
  present complaint*. The client's site keeps looking like the operator's, and
  the operator cannot tell which values are really theirs.
- **Product defaults** — the domain starts as a clean VayuPress install and
  every difference from it was chosen.

**Recommend product defaults.** Falling back to `settings.Defaults` is falling
back to the product, not to another tenant's data. A new domain is a new site.

For the operator who *wants* their house style, a one-time explicit
**"Copy settings from primary"** action on the domain's page — a copy, not a
link, so later edits to the primary do not reach through into a client's site.

### D3 — The scope lives in the URL, never in session state

Two ways to give the console a per-domain mode:

- **Ambient switcher** — a "you are acting on X" control in the top bar. Less
  routing work, and one catastrophic failure mode: the operator believes they
  are on domain A, edits, and has changed domain B. Nothing in the request
  distinguishes the two cases, so nothing can catch it.
- **The domain in the path** — `/os/d/{domainID}/theme`, `/os/d/{domainID}/seo`,
  `/os/d/{domainID}/analytics`. The URL *is* the scope.

**Recommend the path.** A write cannot be mis-scoped when the scope is in the
address that submitted it; the page is bookmarkable and shareable; a screenshot
in a support thread says which site it is; and the client console becomes a
strict subset of the same routes rather than a parallel implementation.

`/os/theme` and friends keep working and mean **the primary**, explicitly.

### D4 — Settings storage: rebuild the primary key

```sql
ALTER TABLE site_settings RENAME TO site_settings_pre_0153;
CREATE TABLE site_settings(scope TEXT NOT NULL DEFAULT '', key TEXT NOT NULL, value TEXT NOT NULL DEFAULT '', updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY(scope,key));
INSERT INTO site_settings(scope,key,value,updated_at) SELECT '',key,value,updated_at FROM site_settings_pre_0153;
DROP TABLE site_settings_pre_0153;
```

Existing rows backfill to `scope=''` — the primary — because that is precisely
what they are. A single-domain install reads byte-identical before and after.
The migration runner takes one statement per physical line.

**The cache must be re-keyed too.** It is currently `map[key]value` with a TTL,
read on every page render. It becomes `map[scope]map[key]value`, or the first
domain to warm it serves its theme to every other domain on the install — a
cross-tenant leak with no database change behind it, and the kind that appears
only under concurrency.

### D5 — Analytics: the event log needs the domain, not just the counter

Migration 080 scoped `analytics_daily` and left `analytics_pageviews` alone —
and the panel, top pages, trending and the overview all read the event log. So
per-domain traffic is half-built: the number on the client's page is scoped, and
every number on the operator's Analytics page is not.

`analytics_pageviews` gets a `domain_id`, attributed **server-side from the host
this install served**, never from anything the beacon sends. The beacon is a
public endpoint; a value it supplies is a value an attacker chooses.

Existing rows backfill to the primary, for the same reason as 080: every event
recorded before this happened on the one domain the install served.

### D6 — Theme, per domain, including generated CSS

`/theme.css` currently resolves one global theme. It becomes host-resolved,
`Vary`-correct, and cached per host. Theme Studio operates under `/os/d/{id}/`
and writes to that domain's scope. Custom CSS is per-domain, which also means
per-domain CSP considerations do not change — it was already operator-supplied.

### D7 — SEO, per domain

`seo_domain.go` already resolves sitemap and robots per host, which is the hard
half. The soft half — default meta description, social card image, title
templates, verification tokens — lives in `site_settings` and moves with D4.
Search-console verification tokens in particular are **per-property**: one
shared token is wrong for every domain but one.

### D8 — What stays shared, stated plainly

"Totally independent" cannot be literally true on one binary and one box, and a
plan that implies otherwise fails in front of a client:

- **One process.** A remote-code-execution bug reaches every domain at once. Row
  scoping is not a sandbox.
- **One machine, one database, one backup.** They fail and recover together.
- **VayuShield** sees one network stack. Rate limits, the jail and the kernel
  tier are install-wide by construction.
- **One DKIM key**, per ADR-0152 §D5. Deliverability is per-domain and correct;
  the key is one key.
- **Users, roles and sessions** are install-wide. A client is confined by
  ADR-0152's audience gate, not by a separate identity system.
- **The binary, updates, migrations, the queue and the scheduler** are one of
  each.
- **Media** is a shared filesystem today. Per-domain media is deliberately *not*
  in this ADR — it needs an ownership check on delete before it can be offered,
  the same blocker that keeps client post-editing out of ADR-0152.

## Phasing

Each phase lands on `main` under `## [Unreleased]`; one release at the end,
after an adversarial pass.

| Phase | Contents | Unlocks |
|---|---|---|
| **1 — Scope type** | `settings.Scope`, required argument, `GetActiveSettings` deleted, all call sites converted, cache re-keyed. **No behaviour change** | Nothing visible. Everything below |
| **2 — Storage** | Migration: `site_settings` PK becomes `(scope,key)`. Reads fall back to `Defaults`, never to primary | Settings can differ per domain |
| **3 — Console shape** | `/os/d/{id}/…` routes, domain resolution middleware, primary keeps its own paths | A place to put per-domain tools |
| **4 — Theme** | Theme Studio under the scope; `/theme.css` host-resolved and cached per host | Independent design |
| **5 — SEO** | Head meta, social cards, verification tokens per scope | Independent search presence |
| **6 — Analytics** | `analytics_pageviews.domain_id`, server-side attribution, panel scoped | Independent, honest traffic |
| **7 — Copy-from-primary** | The explicit one-time copy action | House style without linkage |

Phase 1 before Phase 2 is not negotiable. Adding the column first and the type
second means a window in which reads are unscoped against a scoped table, which
is the leak this ADR exists to close.

## The adversarial pass this design must survive

Written before implementation, so the tests exist before the code:

- **A missed call site.** The whole point of D1. Test: no `Get`/`Set` overload
  exists without a scope, and the unset scope is refused. Mutation: add an
  unscoped convenience wrapper and confirm the suite fails.
- **Cache bleed.** Warm domain A's cache, read domain B, assert B never sees A's
  value. This is the failure that would not appear in single-domain testing at
  all.
- **The empty-string scope.** `''` is the primary's sentinel. `ForDomain("")`
  must refuse rather than resolve to the primary — the same defect already found
  twice in ADR-0152.
- **Write to another domain.** A request under `/os/d/A/` carrying a body naming
  domain B must be refused, not silently rescoped. Silent substitution reports
  success for an attempt to edit someone else's site.
- **The client's own console.** A confined client under ADR-0152 must reach
  their scope and no other, and the new routes must not widen that.
- **Fallback direction.** Assert that an unset key resolves to the compiled-in
  default and *not* to the primary's stored value — the D2 decision, tested
  rather than assumed.
- **The claims.** Every panel sentence that says "this applies to this domain"
  must be false-able by a test. The sentence that opened this ADR is the reason.

## Open decisions

| # | Decision | Recommendation |
|---|---|---|
| 1 | Inheritance or isolation for unset keys | **Isolation** — fall back to product defaults, with an explicit copy action. Inheritance is what produced the complaint |
| 2 | Ambient domain switcher or domain in the URL | **URL.** Ambient scope means an edit can land on the wrong site with nothing in the request to catch it |
| 3 | Per-domain media | **Out of scope here.** Needs an ownership check on delete first |
| 4 | Per-domain users/logins | **No.** ADR-0152's audience gate is the boundary; a second identity system is a second thing to get wrong |
| 5 | Per-domain VayuShield policy | **Later, and narrow.** Per-route policy could scope; rate limits and the kernel tier cannot |
| 6 | What a *new* domain starts as | **A clean install** — product defaults, blog mode, no content |
| 7 | Existing installs on upgrade | **Byte-identical.** Every current row is the primary's; nothing moves |

## Consequences

- ~60 call sites change signature in Phase 1, mechanically, with the compiler
  naming every one.
- One table rebuild, of the three this codebase has needed. The data is
  configuration rather than business records.
- The console grows a route family. The primary keeps its existing paths, so no
  operator muscle memory breaks.
- A per-domain cache is a real memory cost, bounded by domain count — an install
  with thirty clients holds thirty small maps.
- After this, "per-domain X" stops being a feature to build and becomes the
  default that scoping already provides.
