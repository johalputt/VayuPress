-- Migration 118: segments, a site's saved filters.
--
-- A segment is a name and the Explorer filters it stands for (as JSON, each a
-- dimension, an operator and a value), kept for one hosted site or, with '*',
-- for the install, as goals are (migration 114). The Explorer applies a
-- segment's filters with its own, so "returning visitors from a search" is
-- written once and used on every figure.
CREATE TABLE IF NOT EXISTS analytics_segments(id TEXT PRIMARY KEY, domain_id TEXT NOT NULL DEFAULT '*', name TEXT NOT NULL, filters TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_asegments_domain_created ON analytics_segments(domain_id, created_at);
