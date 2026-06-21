CREATE TABLE IF NOT EXISTS embed_cache (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    url         TEXT NOT NULL UNIQUE,        -- canonical input URL (trimmed)
    resolved_url TEXT NOT NULL,              -- final URL after redirects
    title       TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    provider    TEXT NOT NULL DEFAULT '',    -- e.g. "youtube", "vimeo", "og"
    thumb_name  TEXT NOT NULL DEFAULT '',    -- local media filename (empty = none)
    raw_meta    TEXT NOT NULL DEFAULT '{}',  -- JSON blob of OG/oEmbed metadata
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS embed_cache_url ON embed_cache(url);
