-- Migration 113 (down): the indexes of 032, 084 and 112, and no visitor or
-- entry on an event row.
CREATE INDEX IF NOT EXISTS idx_apv_session ON analytics_pageviews(session_id);
CREATE INDEX IF NOT EXISTS idx_apv_domain_created ON analytics_pageviews(domain_id, created_at);
CREATE INDEX IF NOT EXISTS idx_apv_created_session ON analytics_pageviews(created_at, session_id);
DROP INDEX IF EXISTS idx_apv_domain_entries;
DROP INDEX IF EXISTS idx_apv_entries;
DROP INDEX IF EXISTS idx_apv_session_created;
DROP INDEX IF EXISTS idx_apv_domain_created_visitor;
DROP INDEX IF EXISTS idx_apv_created_session_visitor;
ALTER TABLE analytics_pageviews DROP COLUMN entry;
ALTER TABLE analytics_pageviews DROP COLUMN visitor_id;
