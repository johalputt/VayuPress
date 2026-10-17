-- Migration 113: every event row says whose it is, and every visit row is the
-- visit (the definitions are docs/ANALYTICS.md).
-- analytics_pageviews.visitor_id: a site's visitors were its visit count under
-- another name, because the visitor lived only on analytics_sessions, which
-- had no site. Counted from the site's own events now.
-- analytics_sessions becomes the visit: its site, its last event, how many
-- events and page views it holds, and the page it landed on, kept current by
-- the ingest batch as each event arrives. Visits, bounces, the average visit
-- and landing pages are then read from one row per visit, in the range the
-- visit began in, instead of being rebuilt from every event of every visit
-- (a 30-day overview on 426k views: 0.30 s rebuilt, a range read here).
-- Backfilled from the event rows; sessions recorded before this were cut at
-- fixed half-hours and stay as they were cut.
-- vayuanalytics_sessions.domain_id: an engagement or vitals row says which
-- site its page was on, so a site's Performance reads its own; rows before
-- this are the primary's. One statement per line, as the runner requires.
ALTER TABLE analytics_pageviews ADD COLUMN visitor_id TEXT NOT NULL DEFAULT '';
ALTER TABLE analytics_sessions ADD COLUMN domain_id TEXT NOT NULL DEFAULT '';
ALTER TABLE analytics_sessions ADD COLUMN last_at DATETIME;
ALTER TABLE analytics_sessions ADD COLUMN events INTEGER NOT NULL DEFAULT 0;
ALTER TABLE analytics_sessions ADD COLUMN views INTEGER NOT NULL DEFAULT 0;
ALTER TABLE analytics_sessions ADD COLUMN landing TEXT NOT NULL DEFAULT '';
UPDATE analytics_pageviews SET visitor_id=COALESCE((SELECT s.visitor_id FROM analytics_sessions s WHERE s.id=analytics_pageviews.session_id),'');
UPDATE analytics_sessions SET events=a.n, views=a.v, last_at=a.l FROM (SELECT session_id, COUNT(1) AS n, SUM(event_type=1) AS v, MAX(created_at) AS l FROM analytics_pageviews GROUP BY session_id) AS a WHERE a.session_id=analytics_sessions.id;
UPDATE analytics_sessions SET domain_id=f.domain_id FROM (SELECT session_id, domain_id FROM (SELECT session_id, domain_id, ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY created_at, id) AS rn FROM analytics_pageviews) WHERE rn=1) AS f WHERE f.session_id=analytics_sessions.id;
UPDATE analytics_sessions SET landing=f.url_path FROM (SELECT session_id, url_path FROM (SELECT session_id, url_path, ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY created_at, id) AS rn FROM analytics_pageviews WHERE event_type=1) WHERE rn=1) AS f WHERE f.session_id=analytics_sessions.id;
CREATE INDEX IF NOT EXISTS idx_apv_created_session_visitor ON analytics_pageviews(created_at, session_id, visitor_id);
CREATE INDEX IF NOT EXISTS idx_apv_domain_created_visitor ON analytics_pageviews(domain_id, created_at, visitor_id);
CREATE INDEX IF NOT EXISTS idx_asession_visits ON analytics_sessions(created_at, events, views, last_at);
CREATE INDEX IF NOT EXISTS idx_asession_domain_visits ON analytics_sessions(domain_id, created_at, events, views, last_at);
ALTER TABLE vayuanalytics_sessions ADD COLUMN domain_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_va_domain_entry ON vayuanalytics_sessions(domain_id, entry_time);
DROP INDEX IF EXISTS idx_apv_created_session;
DROP INDEX IF EXISTS idx_apv_domain_created;
