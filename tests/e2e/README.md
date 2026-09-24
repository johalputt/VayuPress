# VayuPress E2E tests (Playwright)

End-to-end browser tests for the public site and the VayuOS console.

## What's covered

- **public.spec.js** — homepage + article render, the **sovereignty invariant**
  (no off-host requests), and sitemap/feed availability.
- **console.spec.js** — console pages run their scripts: the media library
  loads, list keyboard shortcuts run, confirmations use the console's own
  dialog, and a change refreshes the page in place.
- **comments.spec.js** — web addresses in reader comments become links, safely.
- **contact.spec.js** — the contact widget's spam honeypot stays hidden even on a
  page with no stylesheet.
- **stillair.spec.js** — the Still Air console: a design lint over every app and
  section at desktop and phone width (no emoji icons, nothing wider than the
  screen, no capitals, no hand-written colours, no doubled headings, no script
  errors), and the command bar, account menu and confirmation dialog operated
  by keyboard alone.

## Running locally

The tests expect a running instance reachable at `BASE_URL`. In CI a
header-injecting proxy supplies the admin `X-API-Key`; locally you can point at
your own instance and pass the key via the same proxy (`scripts/screenshot-proxy`)
or run against a dev server whose admin pages you can already reach.

```bash
cd tests/e2e
npm install
npx playwright install --with-deps chromium

# against the header-injecting proxy (recommended for admin specs):
LISTEN=:8088 UPSTREAM=http://localhost:8080 API_KEY=devkey \
  go run ../../scripts/screenshot-proxy &
BASE_URL=http://localhost:8088 ARTICLE_SLUG=hello-vayupress npx playwright test
```

CI runs the whole suite on every push via `.github/workflows/e2e.yml`.
