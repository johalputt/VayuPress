CREATE TABLE IF NOT EXISTS analytics_revenue(
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    pageview_id TEXT NOT NULL DEFAULT '',
    amount REAL NOT NULL,
    currency TEXT NOT NULL DEFAULT 'USD',
    order_id TEXT NOT NULL DEFAULT '',
    event_name TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_arev_created ON analytics_revenue(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_arev_session ON analytics_revenue(session_id);
