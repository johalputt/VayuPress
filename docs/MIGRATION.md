# VayuPress Migration Guide

Import content from any major platform into VayuPress.

---

## Supported Platforms

| Platform | Tool | Method |
|----------|------|--------|
| Markdown folder | **built-in** | `vayupress migrate markdown` |
| WordPress | `tools/wordpress2vayu` | standalone binary |
| Ghost | `tools/ghost-to-vayu` | standalone binary |
| Hugo | `tools/hugo2vayu` | standalone binary |
| Jekyll | `tools/jekyll2vayu` | standalone binary |
| Medium | `tools/medium2vayu` | standalone binary |
| Notion | `tools/notion2vayu` | standalone binary |
| Substack | `tools/substack2vayu` | standalone binary |

---

## Built-in: Markdown Folder Import

VayuPress v1.1.0+ ships a `migrate` subcommand. It imports any folder of
Markdown files (with optional YAML frontmatter) directly into your running
database.

### Quick Start

```sh
# Preview what would be imported (safe, no DB writes)
vayupress migrate markdown --dir ./posts --dry-run

# List files without importing
vayupress migrate list --dir ./posts

# Import
vayupress migrate markdown --dir ./posts

# Import a specific database file (when not using the default)
vayupress migrate markdown --dir ./posts --db /var/lib/vayupress/vayu.db
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | _(required)_ | Path to the folder containing `.md` files |
| `--db` | `$VAYU_DB` or `./vayupress.db` | Path to the VayuPress SQLite database |
| `--recursive` | `true` | Walk subdirectories |
| `--skip-drafts` | `true` | Skip files with `draft: true` in frontmatter |
| `--dry-run` | `false` | Print what would be imported, no writes |

### Frontmatter

Files may include a YAML frontmatter block at the top:

```markdown
---
title: "My First Post"
slug: my-first-post
date: 2024-03-15
tags: [go, web]
draft: false
---

Content starts here…
```

All fields are optional. Missing values are derived automatically:

| Field | Fallback |
|-------|----------|
| `slug` | filename (slugified) |
| `title` | first `# H1` heading, then the slug |
| `date` | file modification time |
| `tags` | empty |
| `draft` | `false` |

### What Gets Created

For each file the importer creates:

1. A row in `articles` (sanitised HTML content, INSERT OR IGNORE — re-runs are safe)
2. A row in `article_sources` (raw Markdown body, `format = markdown`) — this
   means the Admin v2 editor opens the post in Markdown mode, not HTML mode

### Notes

- Duplicate slugs are silently skipped (`INSERT OR IGNORE`) — re-running after
  an interrupted import is always safe.
- HTML content is sanitised by bluemonday's UGC policy before being stored
  (same path as the write queue).
- The importer uses the application database. If you run it while the server is
  up, VayuPress's WAL mode handles concurrent access safely.

---

## WordPress → VayuPress

**Tool**: `tools/wordpress2vayu`

Reads directly from the WordPress MySQL database (does **not** require an XML
export — avoids lossy intermediate formats).

### Build the tool

```sh
cd tools/wordpress2vayu
go build -o wp2vayu ./cmd/wp2vayu
```

### Usage

```sh
# Count posts to import (sanity check, no writes)
./wp2vayu count \
  --wp-host=localhost --wp-user=root --wp-pass=secret --wp-db=wordpress

# Migrate
./wp2vayu migrate \
  --wp-host=localhost --wp-user=root --wp-pass=secret --wp-db=wordpress \
  --vayu-db=/var/lib/vayupress/vayu.db \
  --dry-run      # remove to write
```

Migrated content includes: posts, published pages, tags, publication dates.
Drafts and private posts are skipped by default (`--include-drafts` to override).

---

## Ghost → VayuPress

**Tool**: `tools/ghost-to-vayu`

Reads from a Ghost MySQL or SQLite database.

```sh
cd tools/ghost-to-vayu
go build -o ghost2vayu ./cmd/ghost2vayu

# SQLite Ghost database (common for self-hosted)
./ghost2vayu migrate \
  --ghost-db=/var/lib/ghost/content/data/ghost.db \
  --vayu-db=/var/lib/vayupress/vayu.db \
  --dry-run
```

---

## Hugo → VayuPress

**Tool**: `tools/hugo2vayu`

Reads Hugo content directories (Markdown + YAML/TOML frontmatter).

```sh
cd tools/hugo2vayu
go build -o hugo2vayu ./cmd/hugo2vayu

./hugo2vayu import \
  --dir=/path/to/hugo/content \
  --vayu-db=/var/lib/vayupress/vayu.db \
  --dry-run
```

---

## Jekyll → VayuPress

**Tool**: `tools/jekyll2vayu`

Reads Jekyll `_posts/` and `_drafts/` directories.

```sh
cd tools/jekyll2vayu
go build -o jekyll2vayu ./cmd/jekyll2vayu

./jekyll2vayu import \
  --dir=/path/to/jekyll/_posts \
  --vayu-db=/var/lib/vayupress/vayu.db
```

---

## Medium → VayuPress

**Tool**: `tools/medium2vayu`

Reads a Medium export ZIP (download from Medium → Account → Export).

```sh
cd tools/medium2vayu
go build -o medium2vayu ./cmd/medium2vayu

./medium2vayu import \
  --zip=/path/to/medium-export.zip \
  --vayu-db=/var/lib/vayupress/vayu.db
```

---

## Notion → VayuPress

**Tool**: `tools/notion2vayu`

Reads a Notion HTML export ZIP.

```sh
cd tools/notion2vayu
go build -o notion2vayu ./cmd/notion2vayu

./notion2vayu import \
  --zip=/path/to/notion-export.zip \
  --vayu-db=/var/lib/vayupress/vayu.db
```

---

## Substack → VayuPress

**Tool**: `tools/substack2vayu`

Reads the Substack posts CSV export (Settings → Exports → Download).

```sh
cd tools/substack2vayu
go build -o substack2vayu ./cmd/substack2vayu

./substack2vayu import \
  --csv=/path/to/posts.csv \
  --vayu-db=/var/lib/vayupress/vayu.db
```

---

## After Importing

1. **Regenerate SEO artefacts** — visit `/admin/v2/seo` and click **Regenerate**.
   This rebuilds `sitemap.xml`, `feed.xml`, and `robots.txt` from the new
   content.

2. **Review imported posts** — visit `/admin/v2/posts` to confirm the import
   looks correct. Use the live search bar to find specific articles.

3. **Check for thin content** — the SEO dashboard reports articles with
   fewer than 300 words ("thin content"). Imported content is unchanged;
   the meter just gives you visibility.

4. **Backup** — the database auto-backs-up before any self-update apply, but
   for a one-off migration it is good practice:
   ```sh
   cp /var/lib/vayupress/vayu.db /var/lib/vayupress/vayu.db.pre-migration
   ```

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `DB init failed` | Wrong `--db` path or DB not initialised | Run `vayupress` once to auto-run migrations, then retry |
| Slug conflicts silently skipped | Duplicate slug in source | Articles with the same slug already exist — they are not overwritten |
| Images not migrated | External image URLs | Images stay at their original URL; re-upload manually via the editor |
| `draft: true` articles missing | `--skip-drafts=true` (default) | Re-run with `--skip-drafts=false` |
