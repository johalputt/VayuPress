# ghost-to-vayu

Migrate a **Ghost CMS database** directly into **VayuPress** — no Ghost admin panel required.

Connect straight to your Ghost MySQL or SQLite database and import all posts into VayuPress's SQLite store. Slugs are preserved exactly. Migration is throttled so it won't hammer your VPS. Interrupted runs resume from the last checkpoint.

---

## Why this tool exists

Ghost's admin API requires a running Ghost instance. When you only have database access (VPS with MySQL or the raw SQLite file), the API route is closed. This tool bypasses Ghost entirely and reads the database directly.

---

## Features

- **Direct DB access** — MySQL and SQLite supported, no Ghost process needed
- **Slug preservation** — Ghost slugs become VayuPress slugs as-is
- **Images & formatting preserved** — Ghost HTML is passed through, so every
  inline Unsplash/Pixabay image, link, heading, and list survives. VayuPress
  sanitizes it (bluemonday) on render, so it's safe by construction.
- **Feature images kept** — a post's hero image is prepended as a `<figure>`
- **All editor formats** — rendered HTML preferred; Mobiledoc (Ghost 1–4) and
  Lexical (Ghost 5) converted to HTML when no rendered html is stored
- **Keyset pagination** — pages by primary key, not `OFFSET`, so every batch
  stays fast and light even 200k rows deep (no full-table scans on your VPS)
- **Throttled batching** — configurable batch size and delay, default 50 / 200 ms
- **Resume on interrupt** — checkpoint saved every batch; `Ctrl+C` resumes safely
- **Idempotent** — re-running skips posts already imported (`INSERT OR IGNORE`)
- **Dry-run mode** — preview conversion without writing anything
- **Tags preserved** — Ghost tag slugs → VayuPress tag CSV, in Ghost sort order

---

## Quick start

### 1. Install

```bash
git clone https://github.com/johalputt/VayuPress
cd VayuPress/tools/ghost-to-vayu
go build -o ghost2vayu ./cmd/ghost2vayu
```

Requires Go 1.22+ and CGO (for SQLite). On Ubuntu/Debian:

```bash
apt install gcc
```

### 2. Count posts first (no writes)

```bash
# MySQL Ghost
./ghost2vayu count \
  --ghost-driver mysql \
  --ghost-dsn "ghost_user:password@tcp(127.0.0.1:3306)/ghost_production"

# SQLite Ghost
./ghost2vayu count \
  --ghost-driver sqlite3 \
  --ghost-dsn "/var/lib/ghost/content/data/ghost.db"
```

### 3. Dry run

```bash
./ghost2vayu migrate \
  --ghost-driver mysql \
  --ghost-dsn "ghost_user:password@tcp(127.0.0.1:3306)/ghost_production" \
  --vayu-db /path/to/vayupress.db \
  --dry-run
```

### 4. Migrate

```bash
./ghost2vayu migrate \
  --ghost-driver mysql \
  --ghost-dsn "ghost_user:password@tcp(127.0.0.1:3306)/ghost_production" \
  --vayu-db /path/to/vayupress.db \
  --status published \
  --batch 50 \
  --delay 300ms
```

For a large dataset (100k+ posts) use a longer delay on a loaded VPS:

```bash
  --batch 25 --delay 500ms
```

---

## All flags

| Flag | Default | Description |
|------|---------|-------------|
| `--ghost-driver` | `mysql` | `mysql` or `sqlite3` |
| `--ghost-dsn` | *(required)* | Connection string for Ghost DB |
| `--vayu-db` | `vayupress.db` | Path to VayuPress SQLite file |
| `--status` | `published` | `published`, `draft`, or `all` |
| `--batch` | `50` | Posts per batch |
| `--delay` | `200ms` | Pause between batches |
| `--resume` | `true` | Resume from checkpoint on restart |
| `--dry-run` | `false` | Parse only, no writes |

---

## How it works

```
Ghost DB (MySQL / SQLite)
        │
        │  SELECT posts + tags  (keyset paginated: WHERE id > ? ORDER BY id)
        ▼
  ghostdb.Reader
        │
        │  html      → passed through (images/links/formatting preserved)
        │  lexical   → HTML  (Ghost 5.x fallback)
        │  mobiledoc → HTML  (Ghost 1–4 fallback)
        │  feature_image → prepended as <figure>
        ▼
  convert.BestContent()
        │
        │  INSERT OR IGNORE into articles  (slug preserved)
        ▼
  VayuPress SQLite  →  bluemonday sanitizes on render
```

Ghost content priority: `html` > `lexical` > `mobiledoc`

VayuPress stores article bodies as HTML and sanitizes them with bluemonday
before display, so passing Ghost's HTML through is both faithful and safe —
`<script>` and other unsafe markup are stripped at render time, while images,
links, headings, and lists are kept.

---

## Ghost DB schema compatibility

Tested against Ghost 3.x, 4.x, and 5.x table structures:

- `posts` — id, title, slug, html, mobiledoc, lexical, published_at, created_at, updated_at, status, feature_image, type
- `tags` — id, slug
- `posts_tags` — post_id, tag_id, sort_order

For MySQL, `parseTime=true` is appended to your DSN automatically if missing.

---

## Resuming after interruption

Press `Ctrl+C` at any time. The tool saves the last processed Ghost post id to a
`ghost2vayu_checkpoint` table in the VayuPress database after every batch. Re-run
the same command — it resumes right after that id (because `--resume` is true by
default). Even without a checkpoint, re-running is safe: posts already imported
are skipped via `INSERT OR IGNORE`.

---

## License

MIT — see [LICENSE](LICENSE)
