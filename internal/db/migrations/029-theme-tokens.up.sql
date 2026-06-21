CREATE TABLE IF NOT EXISTS theme_tokens (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    name    TEXT NOT NULL DEFAULT 'Default',
    tokens  TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
