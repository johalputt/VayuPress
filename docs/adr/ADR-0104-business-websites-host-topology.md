# ADR-0104: Business Websites & Operator-Chosen Host Topology

**Status**: Accepted
**Date**: 2026-07-02
**Author**: @johalputt
**Owner**: Core
**Relates to**: [ADR-0086](ADR-0086-theme-whole-site-design.md), [ADR-0071](ADR-0071-theme-studio.md)

## Context

VayuPress served exactly one thing at the root domain: the blog. But many
operators — a restaurant, a shop, a school, a clinic, a portfolio — need a
*website* first, with the blog as a secondary surface (or none at all). Until
now they had no option inside the product: they either bent the blog homepage
into a pseudo-site or ran a second stack next to VayuPress, losing the
single-binary, single-VPS sovereignty that is the whole point.

Two things had to be true for this to fit VayuPress's ethos:

1. **No new moving parts.** No second server, no page-builder runtime, no
   database schema sprawl — the same SQLite settings store, the same strict
   CSP, the same Go binary.
2. **Existing installs must never change on update.** A running blog at the
   root domain cannot silently become "a business site" because a new version
   shipped. The topology has to be an explicit, remembered operator choice.

## Decision

Add a **business-website engine** (`internal/bizsite`) and a **VayuOS →
Website** studio, driven entirely by three allowlisted settings keys.

### One content model, many design personalities

`bizsite.Content` is a single flat struct (name, tagline, about, offerings with
optional prices, gallery, hours, contact, CTA, hero image, show-blog flag).
`bizsite.Template` is a *design personality* — accent colour, typography,
section treatments — expressed as scoped CSS layered over a shared,
modern-minimalist base (flat colour, hairline rules, system fonts; **no
gradients, no neon**, matching ADR-0086). Eleven templates ship
(restaurant, café, shop, portfolio, agency, school, clinic, salon, gym,
professional firm, hotel), each with believable default content so the operator
edits a real site rather than an empty form. Selecting a template keeps the
operator's content; empty fields fall back to the template's samples.

Rendering is CSP-strict: the page is server-rendered HTML with every operator
string escaped at the render barrier, and the stylesheet is served same-origin
at `/site.css` (base + active template). No inline styles, no CDNs.

### Host topology is an explicit, persisted choice

A new `site.mode` setting selects what the **root domain** serves:

- `""` / `"blog"` — the blog stays at the root (the historic default). This is
  the value every existing install already has (unset → blog), so **an update
  changes nothing**.
- `"business"` — the business site serves at the root; the blog moves to
  `blog.<domain>` and mail stays at `mail.<domain>`.

`handleHome` consults `bizRootActive(r)`: business mode AND a request host that
is not the `blog.` subdomain. This keeps `blog.<domain>` serving the real blog
feed even in business mode, using the request Host header rather than a second
process. The site is always previewable at `/site` regardless of mode, so an
operator builds and polishes before flipping the switch.

Two more allowlisted keys carry the design and content: `biz.template` and
`biz.content` (JSON). All three are gated by `settings.AllKeys` and covered by
the theme-editor drift-guard test's `outOfBand` map, since they are managed in
the Website studio rather than the legacy theme editor.

### Automatic TLS for the whole topology

The deploy script's nginx `server_name` and the Let's Encrypt certificate now
cover the root, `www.`, `blog.` and `mail.` hosts in one certificate, with
graceful SAN fallbacks if a subdomain's DNS is not yet pointed. Certbot's
systemd timer renews it hands-free — no terminal after setup. Operators point
`domain.com`, `blog.domain.com` and `mail.domain.com` at the server; everything
else is automatic.

## Consequences

- A small business gets a real website, a blog, and sovereign mail from one
  binary — no second stack, no page-builder runtime.
- The blog is never orphaned: it simply relocates to a subdomain, and readers
  of `blog.<domain>` are unaffected by the root switch.
- Because the mode is an explicit setting that defaults to the historic
  behaviour, no existing deployment changes on upgrade — the operator opts in.
- Templates are pure CSS personalities over one model, so adding a twelfth is a
  data change, not new plumbing; the catalogue test enforces ≥10 distinct,
  gradient-free templates with default content.
- The CSP posture is unchanged: server-rendered, escaped, same-origin CSS.
