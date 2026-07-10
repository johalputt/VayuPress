# ADR-0127: Brand Refresh, README Showcase Overhaul & Live GitHub-Stars Site Pipeline

**Status**: Accepted
**Date**: 2026-07-10
**Author**: @johalputt
**Relates to**: the marketing/docs site (`docs/site/`), the deploy workflow
(`.github/workflows/deploy-site.yml`), the screenshot pipeline
(`scripts/capture-screenshots.sh`, `.github/workflows/screenshots.yml`), the
brand assets (`docs/assets/`, `docs/site/assets/`), and the VayuShield "Aegis"
bot shield (ADR-0120, ADR-0122, ADR-0123).

## Context

Three presentation gaps had accumulated as the platform grew:

1. **The README under-sold the product.** VayuShield "Aegis" — the built-in,
   self-learning bot shield and anti-DDoS layer that removes the need for
   Cloudflare — was not showcased at all, and the recent writer suite
   (typewriter scrolling, focus spotlight, paste-as-Markdown, live outline,
   footnotes, captions, block duplicate, keyboard reordering), the dashboard
   trend area chart, and the mobile-responsive console were unmentioned.

2. **The website's star count was not truly live.** The site fetched the
   GitHub API with `cache: 'force-cache'`, which pins the first cached response
   and never revalidates — so the visible star count could be arbitrarily
   stale. There was also no rate-limit-proof fallback for visitors whose
   network blocks `api.github.com`.

3. **The brand wordmark** needed to read **VayuPress** with a capital **V** and
   **P** across the logo lockups, in both light and dark variants.

## Decision

### Brand assets

The logo lockups are replaced with new light/dark variants whose wordmark reads
**VayuPress** (capital V and P). The files keep their existing paths so every
reference (README `<picture>`, site, favicons) picks them up unchanged:

- `docs/assets/vayupress-logo-light.png` — white mark + wordmark, for dark
  backgrounds (README dark mode via `prefers-color-scheme: dark`).
- `docs/assets/vayupress-logo.png` — dark mark + wordmark, for light
  backgrounds.
- `docs/site/assets/logo-dark.png` and the `*-mark-*` / favicon variants track
  the same artwork.

Backgrounds should be transparent PNGs so the mark sits on any surface. The
README's existing `<picture>` element already maps dark mode → the light
(white) logo and light mode → the dark logo, so no markup change is required
when the files are swapped.

### README showcase overhaul

The README gains: a dedicated **VayuShield "Aegis"** entry in *What you get*
(the L0–L5 layer summary), a **VayuShield** showcase screenshot
(`docs/screenshots/admin-os-shield.png`, newly captured by the screenshot
pipeline), expanded writer and VayuOS copy, a bot-&-DDoS-protection row in the
comparison table, VayuShield in the architecture diagram, and **live badges**
for the latest release and star count (shields.io endpoints that query the repo
server-side, so they update themselves with no rebuild).

### Live GitHub-stars site pipeline

The star count on `vayupress.com` is made genuinely live via a two-stage,
rate-limit-proof design:

1. **Build-time bake.** `deploy-site.yml` gains a step that fetches
   `stargazers_count` with the workflow token (5,000 req/hr) and writes
   `_site/assets/stars.json`. A **daily `schedule:` cron** redeploys the site so
   this baked value refreshes even when no site files changed.
2. **Client refresh.** `app.js` `fetchStars()` reads the same-origin
   `assets/stars.json` first (instant, never rate-limited, works even when
   `api.github.com` is blocked), then revalidates against the live GitHub API
   with `cache: 'default'` (honouring GitHub's own ~60 s `Cache-Control`)
   instead of the old `force-cache` that pinned a stale copy.

The site continues to deploy via **GitHub Actions** (`actions/deploy-pages`, per
the existing workflow), not a branch push.

## Consequences

- **Positive**: the README now reflects the product's actual capabilities and
  ranks its bot-shield story front and centre; the star count is correct on
  first paint and self-updates both daily (server) and in-browser (client) with
  no anonymous-rate-limit exposure; the brand reads consistently.
- **Neutral**: swapping the logo PNGs is a content operation with no code
  change; the baked `stars.json` is best-effort (a fetch failure leaves the
  client live-fetch as the fallback).
- **Follow-up**: the Bot Shield screenshot lands the next time the screenshots
  workflow runs against a live instance.
