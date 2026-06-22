# Ghost → VayuPress Migration Guide

Migrate your Ghost CMS site to VayuPress v1.7.0. This guide covers the direct
database migration path (recommended for 10k–200k+ posts) and the JSON export
path (simpler, for smaller sites).

---

## Which path to choose

| Scenario | Recommended path |
|----------|-----------------|
| You have VPS/shell access to Ghost's MySQL or SQLite | **Direct DB** (`ghost-to-vayu`) |
| You only have Ghost's JSON export file | **JSON API import** |
| 10k+ posts | **Direct DB** — keyset pagination avoids memory spikes |
| Ghost 5 with Lexical editor | **Direct DB** — tool handles Lexical→HTML |

---

## Path A — Direct Database Migration (Recommended for 200k+ posts)

The `ghost-to-vayu` tool ships with VayuPress and reads Ghost's MySQL or SQLite
database directly — no running Ghost process needed. It uses keyset pagination
(no `OFFSET`), throttled batching, and a resume checkpoint so it can handle
200k+ posts safely on a loaded VPS.

### Step 1 — Build the migration tool

```bash
# On the VPS — use the full path to avoid picking up an old system Go
git clone https://github.com/johalputt/VayuPress.git /tmp/VayuPress
cd /tmp/VayuPress/tools/ghost-to-vayu
/usr/local/go/bin/go build -o /usr/local/bin/ghost2vayu ./cmd/ghost2vayu/
```

> **Note:** always call `/usr/local/go/bin/go` explicitly (or run
> `source /etc/profile.d/go.sh` first) to make sure you are using the Go
> version installed by the deploy script rather than an older system Go.
> The `toolchain` directive in the module requires Go 1.24+.

### Step 2 — Count posts (dry run first)

```bash
# MySQL Ghost
./ghost2vayu count \
  --ghost-driver mysql \
  --ghost-dsn "ghost_user:password@tcp(127.0.0.1:3306)/ghost_production"

# SQLite Ghost (e.g. self-hosted on same VPS)
./ghost2vayu count \
  --ghost-driver sqlite3 \
  --ghost-dsn "/var/lib/ghost/content/data/ghost.db"
```

### Step 3 — Dry run to validate conversion

```bash
./ghost2vayu migrate \
  --ghost-driver mysql \
  --ghost-dsn "ghost_user:password@tcp(127.0.0.1:3306)/ghost_production" \
  --vayu-db /var/lib/vayupress/vayupress.db \
  --status all \
  --dry-run
```

Review the output — any Mobiledoc/Lexical conversion warnings appear here.

### Step 4 — Migrate (published posts first, then drafts)

```bash
# Migrate published posts with conservative throttle for 200k+ rows
./ghost2vayu migrate \
  --ghost-driver mysql \
  --ghost-dsn "ghost_user:password@tcp(127.0.0.1:3306)/ghost_production" \
  --vayu-db /var/lib/vayupress/vayupress.db \
  --status published \
  --batch 25 \
  --delay 300ms

# Then drafts
./ghost2vayu migrate \
  --ghost-driver mysql \
  --ghost-dsn "ghost_user:password@tcp(127.0.0.1:3306)/ghost_production" \
  --vayu-db /var/lib/vayupress/vayupress.db \
  --status draft \
  --batch 25 \
  --delay 300ms
```

**What happens during migration:**
- Each batch of 25 posts is inserted with `INSERT OR IGNORE` (idempotent)
- A checkpoint file (`ghost2vayu.checkpoint`) saves progress after each batch
- `Ctrl+C` is safe — re-run the same command to resume
- At 25 posts/batch × 300ms delay: 200k posts ≈ ~40 minutes

### Step 5 — Verify

```bash
# Count imported articles via VayuPress API
curl -s "https://yourdomain.com/api/v1/articles?limit=1" \
  -H "X-API-Key: YOUR_KEY" | python3 -m json.tool | grep total

# Spot-check a specific post by slug
curl -s "https://yourdomain.com/api/v1/articles/your-post-slug" \
  -H "X-API-Key: YOUR_KEY" | python3 -m json.tool
```

### Step 6 — Rebuild search index and render cache

```bash
# Trigger full search re-index
curl -s -X POST "https://yourdomain.com/os/api/search/reindex" \
  -H "X-API-Key: YOUR_KEY"

# Warm the render cache (optional — pages are lazy-rendered on first request)
curl -s -X POST "https://yourdomain.com/os/api/cache/warm" \
  -H "X-API-Key: YOUR_KEY"
```

---

## Path B — Ghost JSON Export

Use this when you only have the Ghost admin export file (`ghost.json`).

### Step 1 — Export from Ghost

`Settings → Labs → Export content` → downloads `ghost.json`

### Step 2 — Convert to VayuPress bulk format

```python
#!/usr/bin/env python3
# ghost_json_to_vayu.py
import json, sys

with open('ghost.json') as f:
    data = json.load(f)

posts = data['db'][0]['data']['posts']
items = []
for p in posts:
    if p.get('type') != 'post':
        continue
    items.append({
        'title': p.get('title', ''),
        'slug':  p.get('slug', ''),
        'content': p.get('html') or p.get('plaintext') or '',
        'tags':  [],
        'status': 'published' if p.get('status') == 'published' else 'draft',
    })

print(f"Found {len(items)} posts")
with open('vayu_import.json', 'w') as f:
    json.dump(items, f)
```

### Step 3 — Send to VayuPress bulk API in batches

```bash
#!/bin/bash
# Split into 200-item chunks and import sequentially
python3 -c "
import json
with open('vayu_import.json') as f: posts = json.load(f)
for i in range(0, len(posts), 200):
    with open(f'chunk_{i:06d}.json','w') as out:
        json.dump(posts[i:i+200], out)
print('Split complete')
"

for chunk in chunk_*.json; do
  echo "Importing $chunk..."
  curl -s -X POST https://yourdomain.com/api/v1/articles/bulk \
    -H "X-API-Key: YOUR_KEY" \
    -H "Content-Type: application/json" \
    -d @"$chunk" | python3 -m json.tool
  sleep 0.5
done
```

---

## What migrates

| Ghost field | VayuPress field | Notes |
|-------------|----------------|-------|
| `title` | `title` | Exact |
| `slug` | `slug` | Exact — permalinks preserved |
| `html` | `content` | Sanitized by bluemonday on render |
| `mobiledoc` | `content` | Converted to HTML (Ghost 1–4) |
| `lexical` | `content` | Converted to HTML (Ghost 5) |
| `status` | `status` | `published`→`published`, else `draft` |
| `feature_image` | prepended `<figure>` | Inline in content |
| Tags | via `posts_tags` join | Direct DB path only |

## What does NOT migrate (manual steps)

| Ghost feature | VayuPress equivalent | Action needed |
|--------------|---------------------|--------------|
| Members / subscribers | Members panel (`/os/members`) | Export Ghost member CSV, re-import |
| Paid tiers | Members settings | Re-configure in VayuPress |
| Custom themes | Theme Studio (`/os/theme`) | Recreate design tokens |
| Ghost routes (`routes.yaml`) | VayuPress router is fixed | Redirects in Nginx if slugs differ |
| Newsletters | Newsletter panel | Re-configure SMTP in VayuPress |

---

## DNS cutover checklist

1. VayuPress running and all posts verified → `systemctl status vayupress`
2. Nginx configured for your domain — see `deploy/nginx-vayupress.conf`
3. Obtain TLS certificate with the webroot method (VayuPress proxies everything,
   so `--nginx` certbot mode fails unless you add the ACME challenge block first):
   ```bash
   # The nginx config already serves /.well-known/acme-challenge/ from /var/cache/vayupress.
   # Reload nginx first, then:
   sudo certbot certonly --webroot -w /var/cache/vayupress \
     -d yourdomain.com -d www.yourdomain.com \
     --non-interactive --agree-tos -m admin@yourdomain.com
   sudo nginx -t && sudo systemctl reload nginx
   ```
4. Add 301 redirects in Nginx for any changed URL patterns:
   ```nginx
   # Ghost used /tag/:tag — VayuPress uses /tag/:tag (same — no change needed)
   # Ghost author pages: redirect if you don't need them
   location ~ ^/author/ { return 301 /; }
   ```
5. Lower DNS TTL to 300 seconds 24 hours before cutover
6. Update DNS A record to VPS IP
7. Verify with: `curl -s https://yourdomain.com/ | head -5`
8. Restore DNS TTL to 3600 after confirming clean

---

## Performance expectations at 200k posts

Based on benchmarks for SQLite WAL + Go HTTP on a single VPS:

| Metric | Expected |
|--------|---------|
| Article page (cached) | < 5 ms |
| Article page (cold) | < 50 ms |
| Article list API (page 1) | < 10 ms |
| Full-text search (Meilisearch) | < 30 ms |
| Full-text search (SQLite FTS5 fallback) | < 100 ms |
| Migration throughput | ~25 posts/300ms batch ≈ 40 min for 200k |
| Concurrent readers | 10,000+ (WAL allows parallel reads) |
| Write queue throughput | 3,600 writes/s (WAL mode) |

---

## Meilisearch for 200k+ post search (strongly recommended)

SQLite FTS5 works but Meilisearch is faster and more relevant at this scale.

```bash
# Install Meilisearch on the same VPS.
# Use the MUSL static build — the default install.meilisearch.com binary is
# dynamically linked against GLIBC 2.32+ and will crash on Ubuntu 20.04 (GLIBC 2.31).
MEILI_VER=$(curl -fsSL "https://api.github.com/repos/meilisearch/meilisearch/releases/latest" \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['tag_name'])")
curl -fsSL -o /tmp/meilisearch \
  "https://github.com/meilisearch/meilisearch/releases/download/${MEILI_VER}/meilisearch-linux-amd64-musl"
chmod +x /tmp/meilisearch
sudo mv /tmp/meilisearch /usr/local/bin/

# Create systemd unit
cat > /etc/systemd/system/meilisearch.service << 'EOF'
[Unit]
Description=Meilisearch
After=network-online.target

[Service]
User=meilisearch
Group=meilisearch
ExecStart=/usr/local/bin/meilisearch --db-path /var/lib/meilisearch --env production
Restart=always
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

useradd -r -s /usr/sbin/nologin meilisearch
mkdir -p /var/lib/meilisearch && chown meilisearch: /var/lib/meilisearch
systemctl enable --now meilisearch

# Add to VayuPress env:
echo "MEILISEARCH_URL=http://localhost:7700" >> /etc/vayupress/env
echo "MEILISEARCH_KEY=your_master_key" >> /etc/vayupress/env
systemctl restart vayupress

# Trigger full search index build
curl -s -X POST https://yourdomain.com/os/api/search/reindex \
  -H "X-API-Key: YOUR_KEY"
```

---

## Troubleshooting

**"UNIQUE constraint failed: articles.slug"** — Post with that slug already exists. `ghost2vayu` uses `INSERT OR IGNORE` so this is harmless; the existing post is kept.

**Migration stalls at a specific batch** — Ghost Mobiledoc with deeply nested Mobiledoc cards can take > 100ms to convert. Add `--delay 500ms` and reduce `--batch 10`.

**Search returns 0 results after migration** — The search index is built asynchronously. Wait 5 minutes after migration completes, then check `GET /os/api/search/status`.

**Images show as broken links** — Ghost's inline images use absolute URLs to your old domain. Do a find-replace in the DB after migration:
```sql
UPDATE articles
SET content = REPLACE(content, 'https://old-ghost-domain.com/', 'https://new-domain.com/')
WHERE content LIKE '%old-ghost-domain.com%';
```
