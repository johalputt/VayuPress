# VayuPress — project memory

Sovereign, zero-telemetry CMS. Single Go binary, SQLite (WAL), chi router,
strict CSP. Apache-2.0. **Current version: `2.5.0`** (`cmd/vayupress/main.go`).

## Production deployment (johal.in)

- **Scale:** ~234,000 articles, **~12 GB SQLite database**, on a **12 GB-RAM** VPS
  behind nginx. The `articles` table is essentially the whole DB (~50–95 KB per
  post of programmatic SEO content). This scale dominates every design decision.
- **Layout:** service `vayupress` (systemd), binary `/usr/local/bin/vayupress`,
  DB `/var/lib/vayupress/vayupress.db`, static `/var/lib/vayupress/static`,
  app on `127.0.0.1:8080` behind nginx (TLS via Let's Encrypt).
- **Deploy:** `cd /tmp/VayuPress && git pull origin main && sudo BACKUP_DB=0 bash scripts/update-vayupress.sh`
  - **Always pass `BACKUP_DB=0`** — the script's 120 s DB backup *always times
    out* on a 12 GB file. Snapshot manually during quiet windows instead
    (`systemctl stop`, `cp`, `systemctl start`).
- **Health:** `curl http://127.0.0.1:8080/health/{live,ready,db,migrations}`.

## ⚠️ Scale rules — DO NOT VIOLATE (these caused a multi-hour outage)

1. **Reads use the read pool.** Use `dbpkg.Reader()` (not `dbpkg.DB`) for any
   `SELECT`. `dbpkg.DB` is the single **writer** connection; a slow read there
   serializes ALL writes + sessions + admin + VayuOS → the whole site hangs.
2. **Never wrap an indexed column in `COALESCE()` in a predicate.**
   `WHERE COALESCE(is_page,0)=0` / `COALESCE(status,'published')='published'`
   defeat `idx_articles_is_page` / `idx_articles_status` → full scan of 234k rows
   (~11.5 GB). `is_page` is `NOT NULL DEFAULT 0` and `status` is
   `NOT NULL DEFAULT 'published'`, so the COALESCE is unnecessary — use the bare
   column: `WHERE is_page=0`, `status='published'`. Always add a `LIMIT`.
3. **Never run `VACUUM`, `dbstat`, or unindexed full scans on the live box.**
   They read the whole 12 GB DB and thrash a 12 GB-RAM host into swap → hang.
   Do heavy maintenance on a *copy* during a quiet window. (`COUNT(*)` on a
   small/indexed predicate and rowid-limited samples are fine.)
4. Cold renders matter: a deploy restarts the service + purges caches, so right
   after an update the homepage/sitemap/RSS re-render from the DB — these MUST be
   index-friendly + on the read pool or the box hangs on the first hit.

## Conventions

- **Branch:** develop on `claude/dazzling-faraday-n9j6k6`; push to **both** that
  branch AND `main`. `main` moves fast (other agents/CI merge) — expect to
  fetch/merge before pushing; resolve by preferring the more thorough fix.
- **Before pushing Go changes:** `gofmt -w`, `go build ./...`, `go vet ./cmd/vayupress/`,
  `go test ./...` must be clean.
- **CSP is strict:** no inline `<script>`/`<style>` except a nonce'd inline
  script; no `eval`; all JS served same-origin (`/os/static/js/*` from disk, or
  a Go const served via a handler). No external CDNs. SVG image uploads refused.
- **Settings:** keys live in `internal/settings` (`AllKeys` allowlist gates
  writes). Every `AllKeys` key must appear in the legacy theme editor OR the
  `outOfBand` map in `theme_contrast_test.go` (drift-guard test).
- **Migrations:** `internal/db/migrations/NNN-name.up/down.sql`, **one statement
  per line** (the runner splits on newlines). Latest: `046-contact-messages`.
- **JS sanity:** `node -e "new Function(require('fs').readFileSync('<file>','utf8'))"`
  to parse-check before committing (no JS test runner).
- Do NOT put the model id, secrets, or the push token in committed files.

## Feature map (admin = "VayuOS" at `/os`)

- **Posts** `/os/posts` — list/filter/search/date, status toggle, **delete**,
  **bulk** publish/unpublish/delete. Excludes `is_page=1` rows.
- **Pages** `/os/pages` — standalone articles flagged `is_page` (render without
  post chrome via `ArticleMetaOverrides.IsPage`). Quick-create + templates
  (Blank/About/Contact/FAQ), in-menu (`nav.items`) + footer-group placement
  (`footer.config`), delete. Editor has an inline "＋ Page" button.
- **Contact form** — opt-in per page via the `[[contact-form]]` marker (or
  `[[contact-form: custom reply]]`). Render layer injects a CSP-safe widget
  (`render.ContactJS` → `/static/js/contact.js`). `POST /api/v1/contact`
  validates, honeypot-screens, rate-limits, **persists to `contact_messages`**,
  emails the operator via `a.mailer` (VayuMail SMTP), and auto-replies to the
  visitor (toggle `contact.autoreply`; recipient `contact.email`).
- **Messages** `/os/messages` — contact inbox: search + unread/date filters,
  bulk mark-read / clear-read, CSV export, per-message detail, sidebar unread
  badge + dashboard card.
- **Media** `/os/media` — search + type filter, **bulk delete**, per-asset
  **alt text** (`media.alt` settings map), auto-applied as the editor's default
  alt for `/media/...` images.
- **SEO** `/os/seo` — artefact freshness, JSON-LD `BlogPosting` (uses real site
  author/name + image), live **Google snippet preview** in the editor, and
  actionable **health checks** (`evaluateSEOHealth`: sitemap fresh, robots
  present, `Disallow: /`, site-wide `noindex`, canonical domain).
- **Theme Studio** `/os/theme` — Tumblr-style: toggle switches, appearance-first
  layout, sticky quick-jump section navigator, an unsaved-changes (dirty)
  indicator and a leave guard. Theme Store at `/os/theme/store`.
