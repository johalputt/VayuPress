-- Migration 113 (down): the indexes of 084 and 112. The added columns are
-- retained, as in earlier down migrations: nothing reads them once the code
-- that writes them is gone.
CREATE INDEX IF NOT EXISTS idx_apv_domain_created ON analytics_pageviews(domain_id, created_at);
CREATE INDEX IF NOT EXISTS idx_apv_created_session ON analytics_pageviews(created_at, session_id);
DROP INDEX IF EXISTS idx_va_domain_entry;
DROP INDEX IF EXISTS idx_asession_domain_visits;
DROP INDEX IF EXISTS idx_asession_visits;
DROP INDEX IF EXISTS idx_apv_domain_created_visitor;
DROP INDEX IF EXISTS idx_apv_created_session_visitor;
