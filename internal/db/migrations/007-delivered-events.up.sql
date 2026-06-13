CREATE TABLE IF NOT EXISTS delivered_events (event_id TEXT PRIMARY KEY, event_type TEXT NOT NULL, delivered_at DATETIME NOT NULL DEFAULT (datetime('now')));
CREATE INDEX IF NOT EXISTS idx_delivered_events_at ON delivered_events(delivered_at);
