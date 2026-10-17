# Ghost → VayuPress

**Last reviewed:** 2026-10-17, against VayuPress 3.18.7

There are two ways in, and one way out of Ghost's URLs. This guide covers
both imports, what comes across, and the cutover.

| You have | Use | Best for |
|----------|-----|----------|
| The JSON export (Settings → Labs → Export) | `vayupress migrate ghost`, built in | Most sites. Nothing to build. |
| Shell access to Ghost's MySQL or SQLite | `tools/ghost-to-vayu` | Large catalogues; posts whose export has no HTML |

The commands below use the `vp` helper from [MIGRATION.md](MIGRATION.md#before-you-start),
which runs the binary as `www-data` with the service's environment. Take a
backup first: `sudo vayupress backup -out /root/before-ghost.vpbk`.

## From the JSON export

```bash
vp migrate ghost --file /tmp/ghost-export.json --dry-run
vp migrate ghost --file /tmp/ghost-export.json
```

The built-in importer reads each post's `html`, title, slug, status, publication
date and tags (through the export's `posts_tags`). Published posts are
imported. Drafts are skipped unless you pass `--skip-drafts=false`, and then
they are stored as drafts. A post whose export carries no `html`, only
Mobiledoc or Lexical, comes across empty: use the database tool for those.

## From Ghost's database

`ghost2vayu` reads Ghost's tables directly, without a running Ghost. It
renders Mobiledoc (Ghost 1–4) and Lexical (Ghost 5) to HTML when a post has no
stored HTML. It pages by key rather than `OFFSET`, writes in throttled batches,
and keeps a checkpoint, so an interrupted run resumes.

```bash
git clone https://github.com/johalputt/VayuPress.git /tmp/VayuPress
cd /tmp/VayuPress/tools/ghost-to-vayu && go build -o /tmp/ghost2vayu ./cmd/ghost2vayu
```

The module's `go.mod` names the Go it needs. With `GOTOOLCHAIN=auto` (the
default), an older Go downloads it.

Count first, then dry-run:

```bash
# MySQL
/tmp/ghost2vayu count --ghost-driver mysql \
  --ghost-dsn 'ghost:password@tcp(127.0.0.1:3306)/ghost_production'

# SQLite
/tmp/ghost2vayu count --ghost-driver sqlite3 \
  --ghost-dsn /var/lib/ghost/content/data/ghost.db

sudo -u www-data /tmp/ghost2vayu migrate --ghost-driver mysql \
  --ghost-dsn 'ghost:password@tcp(127.0.0.1:3306)/ghost_production' \
  --vayu-db /var/lib/vayupress/vayupress.db --status all --dry-run
```

Then import:

```bash
sudo -u www-data /tmp/ghost2vayu migrate --ghost-driver mysql \
  --ghost-dsn 'ghost:password@tcp(127.0.0.1:3306)/ghost_production' \
  --vayu-db /var/lib/vayupress/vayupress.db \
  --status all --batch 25 --delay 300ms
```

| Flag | Default | |
|------|---------|--|
| `--ghost-driver` | `mysql` | `mysql` or `sqlite3` |
| `--ghost-dsn` | (required) | A MySQL DSN, or the path to `ghost.db` |
| `--vayu-db` | `vayupress.db` | Always pass `/var/lib/vayupress/vayupress.db` |
| `--status` | `published` | `published`, `draft` or `all` |
| `--batch` | `50` | Posts per batch; lower is gentler |
| `--delay` | `200ms` | Pause between batches |
| `--resume` | `true` | Continue from the checkpoint |
| `--dry-run` | `false` | Convert and print; write nothing |

A Ghost post that is not `published` (a draft or a scheduled post) is stored
as a draft, so nothing private becomes public. A scheduled post's date is
kept, but it is not re-scheduled. Publish it from the editor when it is due.

At the default pace (50 posts, then 200 ms), the pauses alone come to about a
minute per 15,000 posts. Converting Mobiledoc or Lexical adds to that, so on a small
VPS sharing the machine with readers, lower `--batch` and raise `--delay`.

## What comes across

| Ghost | VayuPress | |
|-------|-----------|--|
| Title, slug | Title, slug | Exact |
| `html` | Content | Sanitised when each page is drawn, as every post is (`renderContentHTML`) |
| Mobiledoc, Lexical | Content | Rendered to HTML by `ghost2vayu` only |
| Status | Status | `published` stays published; anything else is a draft |
| Publication date | Date | `published_at`, else `created_at` |
| Tags | Topics | Linked on the next restart |
| Feature image | A `<figure>` at the top of the post | `ghost2vayu` only; the image stays at its URL |

What does not come across, and what to do instead:

| Ghost | In VayuPress |
|-------|--------------|
| Members and subscribers | Members are their own system (VayuOS → Members). Export Ghost's member CSV; members re-join. |
| Paid tiers | Audience › Monetization, with Stripe's signed webhook |
| The theme | Halcyon is the default. Theme Studio sets its fonts, colours and layout. |
| `routes.yaml`, custom collections | No equivalent; see the URLs below |
| Newsletters | Newsletter, sending through the built-in mail server or your SMTP |
| Images on Ghost's server | They stay at their old URLs. Upload them before Ghost goes away (below). |

## After the import

The import writes to the database directly. Restart, rebuild search and
redraw pages, as [MIGRATION.md](MIGRATION.md#after-an-import) describes:

```bash
sudo systemctl restart vayupress
curl -X POST -H "X-API-Key: $API_KEY" http://localhost:8080/os/api/search/reindex
curl -X POST -H "X-API-Key: $API_KEY" http://localhost:8080/os/api/storage/refresh-pages
```

Check the count and spot-check a post:

```bash
curl -s "http://localhost:8080/api/v1/articles?limit=1" | grep -o '"total":[0-9]*'
curl -s "http://localhost:8080/api/v1/articles/your-post-slug"
```

**Images.** Ghost's inline images point at the old domain. Copy Ghost's
`content/images` to a host that will stay up, or upload the files to the media
library, then rewrite the URLs in one statement with the service stopped:

```bash
sudo systemctl stop vayupress
sudo -u www-data sqlite3 /var/lib/vayupress/vayupress.db \
  "UPDATE articles SET content = REPLACE(content, 'https://old.example/content/images/', 'https://new.example/media/') WHERE content LIKE '%old.example/content/images/%';"
sudo systemctl start vayupress
```

Then redraw the pages (`refresh-pages`, above).

## URLs

Ghost and VayuPress put the same posts at slightly different addresses:

| | Ghost | VayuPress |
|-|-------|-----------|
| Post | `/my-post/` | `/my-post` |
| Topic | `/tag/go/` | `/tags/go` |
| Author | `/author/name/` | `/author/{id}` |

**VayuPress redirects the first two itself** (since 3.18.8, `ghostURLTarget`):
`/my-post/` answers `301` to `/my-post` when a published post or page has that
slug, and `/tag/go/`, `/tag/go` and `/tags/go/` answer `301` to `/tags/go`. The
query string is kept. Only a `GET` or `HEAD` that would otherwise be a 404 is
redirected, so no address that works today changes, and a draft's address is
never revealed.

Ghost's author pages have no equivalent, because an author's address here is
their id rather than a slug. If old author links matter, send them home from the
HTTPS `server` block in `/etc/nginx/sites-available/vayupress`:

```nginx
location ~ ^/author/[^/]+/?$ { return 301 /; }
```

The installer rewrites that file when it runs again (`--upgrade`), so re-add it
after one; the one-click update does not touch nginx. For a handful of renamed
posts, an exact rule (`POST /api/v1/admin/redirects`) is the simpler tool.

## Cutover

1. Import, restart, check the count, and read a few posts at their new URLs
   on the server (`curl -H "Host: yourdomain.com" http://127.0.0.1:8080/…`).
2. A day before, lower the DNS TTL to 300 seconds.
3. Run the installer with your domain. It issues the certificates, without a
   manual certbot step. Point the A and AAAA records at the new server.
4. Check `https://yourdomain.com/`, a post, a topic and an old Ghost URL.
5. Restore the TTL once traffic has moved.

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| A post is missing | Its slug already existed, or it was a draft | Existing posts are never overwritten; pass `--status all` or `--skip-drafts=false` |
| A post is empty | The export had no `html` | Re-import it with `ghost2vayu` |
| Topic pages are empty | The service has not restarted since the import | Restart it |
| Search finds nothing new | The index has not caught up | `POST /os/api/search/reindex` |
| A batch is slow | Large Mobiledoc documents | `--batch 10 --delay 500ms` |
| Old links return 404 | An author page, or a post that is not published | The author rule above; publish the post |
