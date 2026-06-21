CREATE TABLE IF NOT EXISTS diagram_cache (
    hash       TEXT PRIMARY KEY,           -- sha256 of the diagram source
    svg        TEXT NOT NULL,              -- rendered, sanitised SVG
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
