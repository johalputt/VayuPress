CREATE TABLE IF NOT EXISTS schema_migrations (
    version     TEXT PRIMARY KEY,
    applied_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    checksum    TEXT NOT NULL,
    direction   TEXT NOT NULL DEFAULT 'up'
);
