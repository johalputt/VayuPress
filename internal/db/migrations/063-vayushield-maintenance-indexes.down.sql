-- Reverse 063: drop the maintenance indexes (additive-only, so nothing else to undo).
DROP INDEX IF EXISTS idx_signatures_prune;
DROP INDEX IF EXISTS idx_signatures_promote;
