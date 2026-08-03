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
domain — renders this, on a page about `site.example.test`:

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

## Addendum — D9 and D10 (2026-08-03)

Two follow-ups from the same report: *"I need a per-domain website editor so I
can choose whether to serve that domain as a blog or as a website, and there
must be an option to install and edit websites with AI using the MCP
connector."*

### D9 — Website is a per-site tool

`/os/d/{id}/website` sets what a domain serves at `/` — **Blog**, **Website**
(blog moves to `blog.<host>`), or **Website + /blog** — and edits that site's
content and template.

The serving side already worked. `siteSourceFor` has resolved the active domain
since ADR-0132 Stage 2b, with the deliberate rule that a secondary carrying no
override serves its **own blog** rather than inheriting the primary's website —
inheriting is what once made every client domain serve the studio's bundle. What
was missing was the admin side: `/os/website` resolves by **request host**, and
an operator's admin request carries no secondary host, so it always edited the
primary. The same shape as the content gap — scoping underneath, nothing
surfacing it.

Three rules the implementation holds:

- **A blank mode is displayed as what it actually serves.** To a visitor, blank
  is the blog; a radio group with nothing selected would tell an operator their
  site serves nothing.
- **Fields this form does not render are carried forward, not wiped.** Services,
  gallery and the section heading survive a save. Losing work nobody touched is
  worse than any layout bug.
- **An unknown template resolves to the default rather than being stored.** A
  stored key nothing matches renders an unstyled page later, with nothing
  connecting it to the save that caused it.

`custom` is deliberately not offered: a custom bundle is an upload, not a
choice, and a radio button that puts a domain into a mode serving a 404 is not a
control.

### D10 — The same operations, through the connector

`list_sites`, `get_site`, `update_site` and `list_site_templates` are on the MCP
server under the existing `domains` permission section, so an operator can say
"build the website for client.example" instead of filling one field at a time.

- **One validator, shared.** The tools go through `scopedWebsiteConfig`, the same
  function the console uses. Two validators for one shape is how one of them
  comes to accept a mode the renderer does not know.
- **Omitted is not blank.** Every content field decodes to a pointer, so an
  assistant sending only a new tagline does not wipe the name, about and phone
  on somebody's live website. This is the single most likely way an AI edit
  destroys a client site, and it is a decoding decision rather than a prompt.
- **The primary is refused by name.** `Registry.SetSite` already refuses it;
  the lookup refuses it first, with the reason, so an assistant is not handed an
  opaque error it will retry. `list_sites` marks it `editable_here: false`.

### D11 — The console diagnoses itself

**A fix or a diagnosis an operator cannot reach from VayuOS has not shipped.**

Stated after four consecutive replies ended in a shell command. Diagnosing one
stuck certificate took `nginx -t`, `tail provision.log`, `systemctl status` and
`vayupress domains list` — every one of which this process can determine for
itself. Asking a person to fetch something the console can already read is the
same defect as a control that does nothing: the page knows and does not say.

So a site whose certificate is pending now shows what the console *checked*, in
the order the root helper hits each condition: is the privileged half installed,
would the helper's own host list include this site, does the name resolve, and
what the last run actually reported. Underneath, the provisioning log verbatim —
because a diagnostic an operator cannot verify is one they have to take on trust,
and that log is the artifact that actually answered it on a real install.

Three consequences that follow from the rule:

- **The fix goes in the binary wherever it can**, because the binary is what the
  in-app updater delivers. A root-side shell fix reaches only operators who
  re-run an installer over SSH, which is the thing being ruled out. The API_KEY
  failure is the worked example: the shell fix was correct and reached nobody,
  and moving it into `config.LoadLocalCLI()` made the same repair arrive through
  Update & Backup.
- **Where a step genuinely needs root, the panel requests it and reports what
  happened.** It never instructs.
- **Where something truly cannot be done from the panel** — installing a systemd
  unit on a first deploy — the panel shows the exact command with the reason.
  That is the ceiling, and it belongs on the page rather than in a conversation.

### D12 — A whole authored site, per domain

> "I just need a full-fledged built website like vayupress.com."

A template fills eight fields on a design somebody else drew. vayupress.com is
not that — it is an authored page. So a hosted domain can now serve a **custom
bundle**: a complete static site, uploaded as a zip or written by an assistant,
served exactly as authored.

The storage layer needed nothing. `customsite.Deploy` already confines every
write to an `os.Root`, refuses traversal in archive entries, caps decompressed
size and file count, keeps the previous release for rollback, and is tested
against hostile archives. `customSiteDirFor` already gives each domain its own
directory with the scope validated as hex rather than trusted into a path.

What was missing was, once again, the admin side resolving by **request host**:
`a.customSiteDir(r)` goes through `contentScope(r)`, and an operator's admin
request carries no secondary host, so the upload always landed on the primary.
Here the directory comes from the domain in the path.

Four rules, each pinned by a mutation-tested case:

- **One deploy path.** The upload and `build_site` both go through
  `customsite.Deploy`. A second implementation of the part that must never be
  wrong is how one of them ends up without the confinement.
- **`index.html` is required, not defaulted.** A bundle with no entry point
  deploys cleanly and serves 404 at the root — a site that looks published and
  is not.
- **`custom` is selectable only once something is deployed.** It is absent from
  the mode list on purpose and appears when a bundle exists. Selecting a mode
  with nothing behind it publishes a domain serving 404 at its root, and the
  refusal says *which* problem it is: an unsupported mode and an empty upload
  slot are different situations with different next steps.
- **`build_site` switches the domain to serve what it just built.** Deploying a
  site and leaving the domain on its blog would be the control-that-did-nothing
  defect: the assistant reports success and the visitor sees the old site.

Deploys are deterministic — the same files produce the same archive — so "did
anything change" stays answerable between two publishes.

**Not claimed:** there is no visual page builder. This is authoring, by hand or
by assistant, plus an upload. The template editor remains the no-authoring path.
