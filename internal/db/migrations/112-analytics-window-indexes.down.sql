-- Migration 112 (down): the created_at-only indexes of 031 and 032.
CREATE INDEX IF NOT EXISTS idx_apv_created ON analytics_pageviews(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_asession_created ON analytics_sessions(created_at DESC);
DROP INDEX IF EXISTS idx_apv_created_session;
DROP INDEX IF EXISTS idx_apv_event_created_session;
DROP INDEX IF EXISTS idx_asession_created_visitor;
