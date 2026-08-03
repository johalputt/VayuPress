-- Migration 084: the analytics EVENT LOG learns which domain it happened on
-- (ADR-0153 Phase 6).
--
-- The migration runner executes ONE statement per physical LINE.
--
-- WHY THIS IS SEPARATE FROM 080. Migration 080 put the domain into
-- analytics_daily and analytics_referrers — the rolled-up counters — and left
-- analytics_pageviews alone. But the event log is what powers the Analytics
-- panel, Top pages, the trending widget and the overview. So per-domain traffic
-- was half-built: the number on a client's own page was scoped, and every number
-- on the operator's Analytics page counted every domain on the install together.
--
-- An ALTER is enough here. The primary key is `id`, which is not changing, so
-- there is nothing to rebuild — unlike 080, where the key itself was wrong.
--
-- EXISTING ROWS BACKFILL TO '' — the primary — for the same reason as 080 and
-- 082: every event recorded before this happened on the one domain the install
-- served. A single-domain install reads identically before and after.
--
-- ATTRIBUTION IS SERVER-SIDE, ALWAYS. The column is filled from the host THIS
-- INSTALL RESOLVED for the request, never from anything in the beacon body.
-- /api/v1/analytics/collect is a public, unauthenticated endpoint: a field it
-- supplies is a field an attacker chooses, and a visitor who could choose their
-- own domain_id could write traffic into any client's report on the install.
ALTER TABLE analytics_pageviews ADD COLUMN domain_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_apv_domain_created ON analytics_pageviews(domain_id, created_at);
CREATE INDEX IF NOT EXISTS idx_apv_domain_event ON analytics_pageviews(domain_id, event_type, created_at);
