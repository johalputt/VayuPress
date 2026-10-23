-- Migration 118 (down): the segments go.
DROP INDEX IF EXISTS idx_asegments_domain_created;
DROP TABLE IF EXISTS analytics_segments;
