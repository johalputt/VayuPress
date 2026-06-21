CREATE TABLE IF NOT EXISTS diagram_cache(hash TEXT PRIMARY KEY,svg TEXT NOT NULL,created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')));
