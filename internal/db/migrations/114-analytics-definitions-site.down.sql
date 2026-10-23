-- Migration 114 (down): the site indexes go. The added columns are retained,
-- as in earlier down migrations: nothing reads them once the code that
-- writes them is gone.
DROP INDEX IF EXISTS idx_afunnels_domain_created;
DROP INDEX IF EXISTS idx_agoals_domain_created;
