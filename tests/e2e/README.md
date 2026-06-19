# VayuPress E2E tests (Playwright)

End-to-end browser tests for the public site and the Admin v2 panel.

## What's covered

- **public.spec.js** — homepage + article render, the **sovereignty invariant**
  (no off-host requests), and sitemap/feed availability.
- **admin.spec.js** — Admin v2 dashboard, posts search/empty-state, the editor
  (live preview + word count), the slash-command palette, the SEO dashboard,
  the update checker, and the **strict CSP** (no `unsafe-eval`).

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
