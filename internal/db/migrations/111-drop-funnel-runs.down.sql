-- Migration 111 (down): the empty table as 034 made it.
CREATE TABLE IF NOT EXISTS analytics_funnel_runs(id TEXT PRIMARY KEY,funnel_id TEXT NOT NULL,started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,completed_steps INTEGER NOT NULL DEFAULT 0,visitor_id TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_afr_funnel ON analytics_funnel_runs(funnel_id);
