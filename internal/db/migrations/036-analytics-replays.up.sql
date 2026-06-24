CREATE TABLE IF NOT EXISTS analytics_replays(
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    events_json TEXT NOT NULL DEFAULT '[]',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_arp_session ON analytics_replays(session_id);
CREATE INDEX IF NOT EXISTS idx_arp_expires ON analytics_replays(expires_at);
