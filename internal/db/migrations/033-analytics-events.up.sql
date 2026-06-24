CREATE TABLE IF NOT EXISTS analytics_event_data(id INTEGER PRIMARY KEY AUTOINCREMENT,event_id TEXT NOT NULL,property_key TEXT NOT NULL,property_value TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_aed_event ON analytics_event_data(event_id);
CREATE INDEX IF NOT EXISTS idx_aed_key ON analytics_event_data(property_key);
