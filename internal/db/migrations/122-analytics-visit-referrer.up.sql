-- Migration 122: a visit row carries the referrer it arrived by, as it
-- carries its landing page (migration 113): the referrer of its first page
-- view, a host as the event log keeps it. Referrers, How they found you and
-- every Analytics page narrowed to one source read visits by it; it was found
-- before by a lookup into the event log for every visit, which one Explorer
-- question can afford and a whole page of narrowed lists cannot.
-- Backfilled from the event rows. One statement per line, as the runner
-- requires.
ALTER TABLE analytics_sessions ADD COLUMN referrer TEXT NOT NULL DEFAULT '';
UPDATE analytics_sessions SET referrer=f.referrer FROM (SELECT session_id, referrer FROM (SELECT session_id, referrer, ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY created_at, id) AS rn FROM analytics_pageviews WHERE event_type=1) WHERE rn=1) AS f WHERE f.session_id=analytics_sessions.id AND f.referrer<>'';
CREATE INDEX IF NOT EXISTS idx_asession_domain_referrer ON analytics_sessions(domain_id, created_at, referrer);
