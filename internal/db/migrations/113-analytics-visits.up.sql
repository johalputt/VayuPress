-- Migration 113: every event row says whose it is and whether it began a visit
-- (row 5, Phase 1; plans/vayupress/analytics-shield/ACCURACY.md).
-- visitor_id: a site's visitors were its visit count under another name,
-- because the visitor lived only on analytics_sessions, which has no site.
-- Counted from the site's own rows now. Backfilled from analytics_sessions.
-- entry: the first event of a visit, decided at ingest. Visits, landing pages
-- and the day a visit belongs to are counted from it, so a visit that crosses
-- midnight is one visit on the day it began. Backfilled as the earliest row of
-- each session (ties broken by id); sessions recorded before this were cut at
-- fixed half-hours, and stay as they were cut.
-- The indexes carry what the console's reads take: visitors from the window's
-- index alone; entries through partial indexes small as the visit count; a
-- visit's events from its session. Each replaced index is a prefix of its
-- replacement, so every query it served is still served. One statement per
-- line, as the runner requires.
ALTER TABLE analytics_pageviews ADD COLUMN visitor_id TEXT NOT NULL DEFAULT '';
ALTER TABLE analytics_pageviews ADD COLUMN entry INTEGER NOT NULL DEFAULT 0;
UPDATE analytics_pageviews SET visitor_id=COALESCE((SELECT s.visitor_id FROM analytics_sessions s WHERE s.id=analytics_pageviews.session_id),'');
UPDATE analytics_pageviews SET entry=1 WHERE id IN (SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY created_at, id) AS rn FROM analytics_pageviews) WHERE rn=1);
CREATE INDEX IF NOT EXISTS idx_apv_created_session_visitor ON analytics_pageviews(created_at, session_id, visitor_id);
CREATE INDEX IF NOT EXISTS idx_apv_domain_created_visitor ON analytics_pageviews(domain_id, created_at, visitor_id);
CREATE INDEX IF NOT EXISTS idx_apv_session_created ON analytics_pageviews(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_apv_entries ON analytics_pageviews(created_at, session_id) WHERE entry=1;
CREATE INDEX IF NOT EXISTS idx_apv_domain_entries ON analytics_pageviews(domain_id, created_at, session_id) WHERE entry=1;
DROP INDEX IF EXISTS idx_apv_created_session;
DROP INDEX IF EXISTS idx_apv_domain_created;
DROP INDEX IF EXISTS idx_apv_session;
