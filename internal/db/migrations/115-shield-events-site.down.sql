-- Migration 115 (down): the site indexes go. The added columns are retained,
-- as in earlier down migrations: nothing reads them once the code that writes
-- them is gone.
DROP INDEX IF EXISTS idx_challenges_domain_created_outcome;
DROP INDEX IF EXISTS idx_blocked_domain_created;
