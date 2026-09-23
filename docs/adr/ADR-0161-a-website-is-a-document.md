# ADR-0161 — A website is a document: pages of sections, drafted, revised, published

- **Status:** Accepted; in progress (unreleased)
- **Date:** 2026-09-23
- **Extends** ADR-0154 (one console per site) and ADR-0159 (what a domain serves).

## 0. The report

An audit of the website builder, and the live install, agreed on why a
template site does not impress: the content model froze at its first version.
A business site is one flat record (`bizsite.Content`) rendered as a single page
with fixed sections. There are no pages, no section order, no draft, no history,
and saving is publishing. vayupress.johal.in published a template's sample
content as its real business because nothing distinguished the two.

## 1. Decision

A template website is a **document**: an ordered list of pages, each an ordered
list of typed sections. It is edited as a **draft** and made public by
**publishing**, and every publish is kept as a **revision**.

```text
Document { v, pages: [ Page { slug, title, description, in_nav, sections: [ Section ] } ] }
Section  { id, kind, ...fields for that kind }
kinds:     hero · text · items · gallery · contact
```

### 1.1 Why typed sections, not free HTML blocks

Every field is plain text or a validated URL, and the renderer escapes all of
it. A site document therefore cannot carry script, style or markup, which is
what lets it stay under the install's strict Content-Security-Policy with no
per-site exception, and what lets a client, the operator and an assistant all
edit it through the same validator. Free-form HTML remains available where it
belongs: the uploaded bundle (`custom` mode) and the post block editor.

### 1.2 Why the database, not a compiled bundle

Published revisions are rows. The public page renders the domain's newest
published revision, as it rendered `bizsite.Content` before. Restoring is
publishing an older revision, so history is as deep as retention allows rather
than the uploaded bundle's single previous generation, and a draft preview is
the same renderer pointed at the draft.

### 1.3 Existing sites

A site with no document renders from its legacy `bizsite.Content` through
`FromLegacy`, which produces the equivalent document. Nothing changes the day
this ships. The first save from the new editor stores a real document; a
render-parity test holds `FromLegacy` to the same visible text, in the same
order, with the same links, as the old renderer.

### 1.4 Pages and the blog

The home page is the page with an empty slug. Other pages are served at
`/<slug>`. Where the blog shares the host (`business_subpath`, `custom`), posts
also live at `/<slug>`, so a page slug that names an existing post, or a
reserved route, is refused at save time. The editor says which.

### 1.5 Contact

The contact section posts to the existing `/api/v1/contact` endpoint (honeypot,
per-address limit, length checks). Messages gain the domain they were sent from
and go to that site's own contact address when it has one. Before this, a form
on a hosted client's site would have landed, unlabelled, in the operator's inbox.

## 2. What every published page carries

Title and description from the page, canonical URL, Open Graph and Twitter
cards, JSON-LD for a local business, the site's icon, and an `alt` on every
image — required by the validator for gallery images, so the audit's "forced
empty alt text" cannot recur.

## 3. Limits

A document is at most 256 KiB of JSON, 20 pages, 40 sections a page, 60 items
or images a section. Slugs match `^[a-z0-9][a-z0-9-]{0,62}$`. Links are
`https:`, `http:`, `mailto:`, `tel:`, a site path (`/…`) or a fragment (`#…`);
images are the same origin or `https:`. Everything else is refused with the
field that failed.

## 4. Consequences

- One editor serves the primary site and every hosted site; the connector edits
  the same document through the same validator.
- `bizsite.Render` stays only as the parity reference for legacy content.
- Templates keep their CSS; the section renderer emits the same `vb-*` markup,
  so every existing template styles the new pages without change.
