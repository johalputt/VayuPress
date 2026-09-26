-- Migration 098 (down): back to the Shield indexes of migration 055.
CREATE INDEX IF NOT EXISTS idx_challenges_created ON vayushield_challenges(created_at DESC);
DROP INDEX IF EXISTS idx_challenges_created_outcome;
DROP INDEX IF EXISTS idx_signatures_queue;
DROP INDEX IF EXISTS idx_signatures_learned;
