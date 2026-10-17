-- Migration 111: analytics_funnel_runs was created by 034 and never written;
-- funnels are computed from page views when asked. A table that holds
-- visitor_id and no code fills is a retention promise nobody keeps.
DROP INDEX IF EXISTS idx_afr_funnel;
DROP TABLE IF EXISTS analytics_funnel_runs;
