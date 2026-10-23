-- Migration 114: a site on every goal and funnel (row 5, audit A2).
--
-- A goal or funnel belonged to no site, so a site's owner could not have one
-- and the install's were counted over every site's traffic. Each definition
-- now names its site: a site's id, or '*' for the install-wide definitions,
-- which every existing row was and still is (they keep counting what they
-- counted).
ALTER TABLE analytics_goals ADD COLUMN domain_id TEXT NOT NULL DEFAULT '*';
ALTER TABLE analytics_funnels ADD COLUMN domain_id TEXT NOT NULL DEFAULT '*';
CREATE INDEX IF NOT EXISTS idx_agoals_domain_created ON analytics_goals(domain_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_afunnels_domain_created ON analytics_funnels(domain_id, created_at DESC);
