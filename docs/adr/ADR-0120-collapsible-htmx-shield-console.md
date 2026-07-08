# ADR-0120: Collapsible, HTMX-Live Bot Shield & Analytics Console

**Status**: Accepted  
**Date**: 2026-07-08  
**Author**: @johalputt  
**Relates to**: [ADR-0036](ADR-0036-csp-nonce-centralized.md) (strict admin CSP), [ADR-0107](ADR-0107-self-hosted-htmx.md) (self-hosted HTMX), [ADR-0111](ADR-0111-vayushield-bot-protection-and-analytics.md) (VayuShield + VayuAnalytics), [ADR-0117](ADR-0117-vayuos-read-path-resilience.md) (off-request dashboard computation)

## Context

The Bot Shield & Analytics console (`/os/shield`) had grown into a long, dense
page: a status hero, a settings form, network-hardening notes, bot-signature
stats, a review queue, a recent-blocks list, and several engagement-analytics
cards, all expanded at once. Two operator asks followed:

1. **Make it calm** — every section should be collapsed by default and expand
   only on click, keeping the top (status) always visible, using the same
   lightweight mechanism the "Network hardening" panel already used, with **no
   heavy code**.
2. **Make it live** — each section should update on its own without reloading
   the whole page, and HTMX should be used wherever else it fits.

The hard constraint is the strict admin CSP (ADR-0036): `script-src 'self'`,
`style-src 'self'`, no inline scripts or styles. Any solution must add no new
client JavaScript and no inline style.

## Decision

Use two mechanisms already present in the codebase, so nothing new ships to the
browser:

### 1. Native `<details>/<summary>` for collapse (zero JS, zero new CSS behaviour)

Every section below the status hero is a `<details class="card">` with a
`<summary class="card-title vs-summary">`, collapsed by default and expanded on
click — exactly the pattern the Network-hardening panel already used, reusing
the existing `.vs-summary` styling. The page header and the live status hero
stay outside any `<details>`, always visible. This is pure HTML + the browser's
built-in disclosure behaviour; the only CSS added is a one-line rule to
right-align the per-section refresh control.

### 2. HTMX for per-section live updates (HTMX already loaded, CSP-safe)

HTMX is already vendored and loaded on every admin page (ADR-0107), so partial
updates cost no new script. Each section's body is wrapped in a container with a
stable id (`vs-body-hero`, `vs-body-protection`, `vs-body-signatures`,
`vs-body-queue`, `vs-body-blocks`, `vs-body-engagement`) whose contents are
produced by a dedicated per-section body builder. A single admin-gated fragment
endpoint, `GET /os/shield/section/{name}`, returns just one section's body HTML
(no page layout), and the same builder feeds both the initial full-page render
and the fragment — one source of truth, no drift.

- **Status hero** polls itself (`hx-trigger="every 10s"`) and also listens for a
  `vs-refresh` event, so live metrics stay current on their own.
- **Signatures, Review queue, Recent blocks, Engagement** each carry a small
  **↻ Refresh** button (`hx-get` the section, `hx-swap="innerHTML"` into its own
  body id).
- **Saving settings** no longer returns `HX-Refresh` (a whole-page reload); the
  handler now returns `HX-Trigger: vs-refresh`, which the hero and the settings
  body listen for — so only those two reload in place, showing the applied state.
- **Confirm/Dismiss** in the review queue still deletes the row on a 2xx
  (`hx-swap="delete"`) and additionally returns `HX-Trigger: vs-refresh-sig`, so
  the signature counts and queue body refresh in place (the "Pending review"
  figure stays accurate).

Fragments are written by a small `writeOSFragment` helper that sets the admin
no-cache/`noindex` headers but, unlike `writeOSHTML`, does not re-issue a CSRF
cookie — the frequent hero poll should not churn the cookie. CSRF for the write
actions (settings/verify/dismiss) is unchanged: the double-submit cookie is set
on the full-page load and mirrored into the request header by the shell's
existing `htmx:configRequest` hook.

## Consequences

- The console opens tidy: only the header + live status hero are visible; the
  operator expands only what they need.
- Sections update independently and cheaply: the hero self-refreshes, and every
  data section can be refreshed (or auto-refreshes after a relevant action)
  without a full-page reload — matching the responsiveness the operator asked
  for.
- The heavy engagement analytics remain served from the off-request background
  cache (ADR-0117), so a section refresh can never run a blocking scan on the
  request path — no reintroduced 502 risk.
- No new client JavaScript and no inline style were added; the strict admin CSP
  (ADR-0036) is fully preserved. The change is additive and presentational plus
  transport — the underlying settings, signature, and analytics behaviour is
  unchanged.
