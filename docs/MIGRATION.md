# Moving to VayuPress

**Last reviewed:** 2026-10-17, against VayuPress 3.18.7

WordPress, Ghost and Markdown import with the binary you already have. Hugo,
Jekyll, Medium, Notion and Substack have standalone importers in `tools/`.
Moving between two VayuPress installs, such as clearnet and a Tor Space, uses a
content bundle.

| Coming from | Use | Reads |
|-------------|-----|-------|
| WordPress | `vayupress migrate wordpress` | The WXR export (Tools → Export → All content) |
| WordPress, from its database | `tools/wordpress2vayu` | MySQL directly, in paced batches |
| Ghost | `vayupress migrate ghost` | The JSON export (Settings → Labs → Export) |
| Ghost, from its database | `tools/ghost-to-vayu` | MySQL or SQLite directly |
| A folder of Markdown | `vayupress migrate markdown` | `.md` files with optional front matter |
| Hugo | `tools/hugo2vayu` | The site's `content/` |
| Jekyll | `tools/jekyll2vayu` | The site's `_posts/` |
| Medium | `tools/medium2vayu` | Medium's account export, as a ZIP or a folder |
| Notion | `tools/notion2vayu` | An HTML export, as a ZIP or a folder |
| Substack | `tools/substack2vayu` | `posts.csv` from the export |
| Another VayuPress | `vayupress migrate export` / `import` | A `.vaybundle` |

Every importer is safe to run twice. A post whose slug already exists is
skipped, never overwritten. The standalone tools also keep a checkpoint and
resume where an interrupted run stopped.

## Before you start

Take a backup you can restore:

```bash
sudo vayupress backup -out /root/before-import.vpbk
```

The built-in importer reads the same configuration as the service, `API_KEY`
and `DB_PATH` included, and writes to that database. Run it as the service user
with the service's environment, so every file it creates belongs to
`www-data`:

```bash
vp() {
  sudo systemd-run --uid=www-data --pipe --wait --quiet \
    -p EnvironmentFile=/etc/vayupress/env /usr/local/bin/vayupress "$@"
}
```

The export file must be readable by `www-data`. `/tmp` works.

## WordPress and Ghost, built in

```bash
vp migrate wordpress --file /tmp/wordpress.xml --dry-run   # what would be imported
vp migrate wordpress --file /tmp/wordpress.xml

vp migrate ghost --file /tmp/ghost-export.json --dry-run
vp migrate ghost --file /tmp/ghost-export.json
```

Titles, slugs, publication dates, tags (WordPress categories too) and draft
status come across. HTML is sanitised (bluemonday's UGC policy) before it is
stored. Drafts are skipped unless you pass `--skip-drafts=false`, and are then
stored as drafts, never published.
Images stay at their original URLs. Upload them to the media library if the
old site is going away.

## A folder of Markdown

```bash
vp migrate list --dir /tmp/posts                 # the files it would read
vp migrate markdown --dir /tmp/posts --dry-run
vp migrate markdown --dir /tmp/posts
```

| Flag | Default | |
|------|---------|--|
| `--dir` | (required) | The folder |
| `--recursive` | `true` | Walk subfolders |
| `--skip-drafts` | `true` | Skip files with `draft: true` |
| `--dry-run` | `false` | List what would be imported; write nothing |

Front matter is optional:

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

| Field | Without it |
|-------|------------|
| `slug` | The file name, slugified |
| `title` | The first level-one heading, then the slug |
| `date` | The file's modification time |
| `tags` | None |

The Markdown is kept beside the rendered HTML (`article_sources`), so the
editor opens the post as Markdown.

## The standalone importers

Each tool is its own Go module under `tools/`. Build it on any machine with Go
and a C compiler, then run it on the server as `www-data`, pointing
`--vayu-db` at the live database. It writes in batches through SQLite's WAL,
alongside the running service.

```bash
cd tools/wordpress2vayu && go build -o /tmp/wp2vayu ./cmd/wp2vayu
```

| Tool | Build | Run |
|------|-------|-----|
| WordPress | `./cmd/wp2vayu` | `wp2vayu migrate --wp-dsn='user:pass@tcp(127.0.0.1:3306)/wordpress' --vayu-db=…` |
| Ghost | `./cmd/ghost2vayu` | `ghost2vayu migrate --ghost-driver=sqlite3 --ghost-dsn=/var/lib/ghost/content/data/ghost.db --vayu-db=…` |
| Hugo | `./cmd/hugo2vayu` | `hugo2vayu import --site=/srv/hugo --vayu-db=…` |
| Jekyll | `./cmd/jekyll2vayu` | `jekyll2vayu import --site=/srv/jekyll --vayu-db=…` |
| Medium | `./cmd/medium2vayu` | `medium2vayu import --input=medium-export.zip --vayu-db=…` |
| Notion | `./cmd/notion2vayu` | `notion2vayu import --zip=notion-export.zip --vayu-db=…` |
| Substack | `./cmd/substack2vayu` | `substack2vayu import --csv=posts.csv --vayu-db=…` |

`--vayu-db` is `/var/lib/vayupress/vayupress.db`. Some tools default to
another name, so always pass it. Every tool takes `--dry-run`. Hugo, Jekyll,
Medium and Substack take `--skip-drafts`, and WordPress and Ghost take
`--status`. The two database readers also take `--batch` (default 50) and
`--delay` (default 200 ms between batches); lower the batch and raise the
delay if a large import competes with readers on a small VPS. Each tool's
`--help` lists the rest. `count` (WordPress, Ghost) and `list` (the others)
show what would be read.

```bash
sudo -u www-data /tmp/wp2vayu migrate --wp-dsn='…' \
  --vayu-db=/var/lib/vayupress/vayupress.db --dry-run
```

## Between two VayuPress installs

```bash
vp migrate export --file /tmp/site.vaybundle                 # posts and pages
vp migrate import --file /tmp/site.vaybundle --dry-run
vp migrate import --file /tmp/site.vaybundle --mode=merge    # or add-only
```

A bundle carries content only: no accounts, mailboxes or keys. It carries a
SHA-256 of its posts, and an import whose checksum does not match is refused
before anything is written. `merge` updates posts whose slug
already exists; `add-only` skips them.

## After an import

The importers write to the database directly, so the running service learns
of the new posts in three steps:

1. **Restart it:** `sudo systemctl restart vayupress`. At start it links
   every post missing from `article_tags` to its topics
   (`StartArticleTagsBackfill`), which topic pages read.
2. **Rebuild search:** the periodic reconciler finds the new posts on its
   own; to index them now,
   `curl -X POST -H "X-API-Key: $API_KEY" http://localhost:8080/os/api/search/reindex`.
3. **Redraw pages:** VayuOS → Storage → **Refresh every page**, also in the
   command palette, or `POST /os/api/storage/refresh-pages`. It rebuilds the
   sitemap, feed and `robots.txt` at once, and the pages in the background at
   the pace the server can spare.

Then open **VayuOS → Posts** to look the import over, and **SEO** for
redirects from the old URLs if they differ.

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `required env not set … API_KEY` | Run without the service's environment | Use the `vp` function above |
| `attempt to write a readonly database` | Run as a user that cannot write the database | Run as `www-data` |
| The service fails to open the database after an import | An import run as root created a `-wal`/`-shm` owned by root | `sudo chown www-data:www-data /var/lib/vayupress/vayupress.db*` |
| A post is missing | Its slug was already taken, or it was a draft | Rename the existing post, or pass `--skip-drafts=false` |
| Topic pages are empty | The service has not restarted since the import | Restart it |
| Search finds nothing new | The reconciler has not run yet | Rebuild the index (step 2) |
| Images are broken | They still point at the old site | Upload them and edit the posts |

A Ghost-specific walk-through, from export to redirects, is in
[GHOST-MIGRATION.md](GHOST-MIGRATION.md).
