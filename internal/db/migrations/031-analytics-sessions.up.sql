CREATE TABLE IF NOT EXISTS analytics_sessions(id TEXT PRIMARY KEY,visitor_id TEXT NOT NULL,browser TEXT NOT NULL DEFAULT '',os TEXT NOT NULL DEFAULT '',device TEXT NOT NULL DEFAULT '',screen TEXT NOT NULL DEFAULT '',language TEXT NOT NULL DEFAULT '',country TEXT NOT NULL DEFAULT '',region TEXT NOT NULL DEFAULT '',city TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_asession_visitor ON analytics_sessions(visitor_id);
CREATE INDEX IF NOT EXISTS idx_asession_created ON analytics_sessions(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_asession_country ON analytics_sessions(country);
