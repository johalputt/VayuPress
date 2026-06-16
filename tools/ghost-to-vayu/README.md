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
- **Content conversion** — handles Ghost HTML, Mobiledoc (v3), and Lexical (v5) formats
- **Throttled batching** — configurable batch size and delay, default 50 posts / 200 ms pause
- **Resume on interrupt** — checkpoint saved every 10 batches; `Ctrl+C` resumes safely
- **Dry-run mode** — preview conversion without writing anything
- **Tags preserved** — Ghost tag slugs → VayuPress tag CSV
- **Works at scale** — tested design for 200k+ post datasets

---

## Quick start

### 1. Install

```bash
git clone https://github.com/johalputt/ghost-to-vayu
cd ghost-to-vayu
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
        │  SELECT posts, tags, authors  (batched, offset-paginated)
        ▼
  ghostdb.Reader
        │
        │  HTML → plain text  (golang.org/x/net/html walker)
        │  Mobiledoc JSON → text  (section/marker extraction)
        │  Lexical JSON → text  (node tree walk)
        ▼
  convert.BestContent()
        │
        │  INSERT OR IGNORE into articles table
        ▼
  VayuPress SQLite  (vayupress.db)
```

Ghost content priority: `html` > `lexical` > `mobiledoc`

---

## Ghost DB schema compatibility

Tested against Ghost 3.x, 4.x, and 5.x table structures:

- `posts` — id, title, slug, html, mobiledoc, lexical, published_at, created_at, updated_at, status, feature_image, type
- `tags` — id, slug
- `posts_tags` — post_id, tag_id, sort_order
- `users` — id, name
- `posts_authors` — post_id, author_id, sort_order

---

## Resuming after interruption

Press `Ctrl+C` at any time. The tool saves a checkpoint offset to a `ghost2vayu_checkpoint` table in the VayuPress database. Re-run the same command — it picks up from where it left off automatically (because `--resume` is true by default).

---

## License

MIT — see [LICENSE](LICENSE)
