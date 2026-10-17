-- Migration 112: the reports Home and the live panel ask for read their window
-- from an index alone. Each counts sessions or visitors over a date range, and
-- with created_at alone indexed SQLite fetched every row in the range from the
-- table to read its session_id or visitor_id: on a copy holding johal.in's
-- 532k page views, Home's four figures took 95, 76, 27 and 24 ms of the 0.35 s
-- the page took. With session_id and visitor_id carried beside created_at
-- they take 26, 17, 20 and 20 ms (3cm).
-- The two created_at-only indexes are replaced, not kept beside: the new ones
-- lead with created_at, so every range they served is still served. A page
-- view written updates one index more than before, a session none more; both
-- are written in batches off the request. One statement per line, as the
-- runner requires.
CREATE INDEX IF NOT EXISTS idx_apv_created_session ON analytics_pageviews(created_at, session_id);
CREATE INDEX IF NOT EXISTS idx_apv_event_created_session ON analytics_pageviews(event_type, created_at, session_id);
CREATE INDEX IF NOT EXISTS idx_asession_created_visitor ON analytics_sessions(created_at, visitor_id);
DROP INDEX IF EXISTS idx_apv_created;
DROP INDEX IF EXISTS idx_asession_created;
