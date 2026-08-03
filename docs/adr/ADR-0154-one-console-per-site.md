# ADR-0154 — One console per site

- **Status:** Accepted
- **Date:** 2026-08-03
- **Supersedes in part:** ADR-0153 (which built the scoping; this builds the surface)
- **Related:** ADR-0132 (VayuDomains), ADR-0152 (agency hosting)

## The report

> "I am opening this subdomain control and they are showing main johal.in
> domain control. I want every subdomain treated as a separate individual
> entity where I can control their website or their blog, whatever I want.
> Also the domain pages are so messy — make them clean, simple, easy to use."

Two complaints, and both are accurate.

## What was actually there

ADR-0153 scoped the *machinery*: settings carry a scope, theme and SEO and
analytics read through it, and `/os/d/{id}` exists. That work is sound. What it
did not do is fix the surface an operator actually walks through, and the surface
still points the other way.

`/os/domains/{id}` — the page titled **Manage site**, reached by clicking a
domain — renders this, on a page about `test.johal.in`:

```html
<p class="text-sm muted">The links below are the <b>install-wide</b> tools.
   They edit the primary site.</p>
<div class="vm-row">
  <a class="btn" href="/os/theme">Theme Studio</a>
  <a class="btn" href="/os/website">Website settings</a>
  <a class="btn" href="/os/analytics">Analytics</a>
  <a class="btn" href="/os/seo">SEO</a>
</div>
```

Four buttons with the names of the four tools an operator wants, on the page
about their client's site, opening the operator's own. The one-line disclaimer
above them is true and is not enough: a caveat in grey type does not survive
contact with a button that looks like the thing you came for. This is the whole
of the reported complaint, and it is a design defect rather than a scoping one.

Three further problems, measured rather than recalled:

1. **Two editors for the same four fields.** The Manage-site page carries a
   **Branding** card writing site name / tagline / description / accents into
   the `config_json` overlay, while `/os/d/{id}/settings` writes the same fields
   into the scoped settings store. Migration 083 moved the overlay into the
   store; the card was never retired. Whichever an operator uses last is not
   defined by anything either page tells them.

2. **Three surfaces for one thing.** `/os/domains` (a card grid),
   `/os/domains/{id}` (a stack of `card-title` cards), `/os/d/{id}` (a link
   list). None of them follows the house style, and an operator has to learn
   which of three pages holds which control.

3. **No content per site at all.** `articles.domain_id` has existed since
   migration 060 and the public site routes on it, but the console offers
   exactly one content control per domain: *"Move a published post to this site
   by its slug."* There is no list of a domain's posts, no way to write one for
   it, no pages, no media. "Control their website or their blog" is not
   partially built — it is absent.

## Decisions

### D1 — One address per site

`/os/d/{id}` is the console for a site. `/os/domains/{id}` redirects to it
permanently. There is one page to learn, its URL names the site, and there is no
second place a control might be hiding.

### D2 — No install-wide link ever appears on a per-site page

Not demoted, not caveated — absent. The operator's own tools live in the
operator's own navigation, which is always one click away and never ambiguous.
A test enforces this: any `href="/os/<tool>"` in per-site markup fails the build.

The general rule this expresses, which has now cost two bugs: **a control that
acts on something other than the page it is on does not belong on that page.**

### D3 — One editor per field

The Manage-site Branding card is retired. `/os/d/{id}/settings` is the only
editor for identity, and Theme Studio the only editor for colour. The
`/os/api/domains/{id}/brand` endpoint stays for one release, writing through to
the scoped store so an operator's bookmark or script does not silently diverge,
and is then removed.

### D4 — Content is a per-site tool

`/os/d/{id}/content` lists this domain's posts and pages, creates a post that is
*born* on this domain, and moves one in or out. It reuses the existing editor:
a second content pipeline would be a second place for every future content
change to be forgotten, which is the mistake D1 exists to stop repeating.

Creating from a scoped console sets `domain_id` from the **path**, never from a
form field — the same rule as every other scoped write (ADR-0153 D3). A body
naming a different domain is refused, not rescoped.

### D5 — The house style, applied

Both pages follow `admin_os_monetization.go` per the standing rule: page header,
`stat-grid` of four tiles answering "what is the state of this site", section
heads, and `monAcc` accordions — pure-CSS `<details>`, CSP-safe, keyboard
reachable, each summary carrying a chip so state reads while collapsed.

The tiles are chosen to answer the question an operator actually has when they
open a client's site, not to fill four boxes.

### D6 — What stays shared, stated on the page

Unchanged from ADR-0153 and repeated because an operator selling this needs the
ceiling in the same view as the capability: one process, one machine, one
database, one mail signing key, one bot shield. Row scoping is not a sandbox.

## Phases

| # | Phase | Ships |
|---|-------|-------|
| 1 | Fold `/os/domains/{id}` into `/os/d/{id}`; delete the install-wide links and the duplicate Branding card | D1, D2, D3 |
| 2 | Rebuild the console in the house style | D5, D6 |
| 3 | Content per site — list, create, move | D4 |
| 4 | Rebuild `/os/domains` as the site list | D5 |
| 5 | Adversarial pass, then one release | — |

## What this does not claim

- It does not isolate sites from each other at runtime. One process serves them
  all; a bug in it reaches every site at once.
- It does not give a client their own console. ADR-0152's confined client login
  is a separate, narrower surface and stays that way.
- Media, comments, newsletter and monetization remain install-wide. They are
  listed on the console with that status rather than linked, for the same reason
  D2 exists.
