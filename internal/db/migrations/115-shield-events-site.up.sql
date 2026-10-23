-- Migration 115: a site and a request on every shield event.
--
-- A block or a challenge recorded neither the site it was for nor the request
-- that drew it, so an install serving many sites could not say which of them
-- was under attack, and an event could not be traced to its log line. Both are
-- recorded from now on. Rows written before this migration carry '*' for the
-- site: which site they were for was never recorded, and they are not
-- attributed to the primary by guesswork.
ALTER TABLE vayushield_blocked ADD COLUMN domain_id TEXT NOT NULL DEFAULT '*';
ALTER TABLE vayushield_blocked ADD COLUMN request_id TEXT NOT NULL DEFAULT '';
ALTER TABLE vayushield_challenges ADD COLUMN domain_id TEXT NOT NULL DEFAULT '*';
ALTER TABLE vayushield_challenges ADD COLUMN request_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_blocked_domain_created ON vayushield_blocked(domain_id, created_at);
CREATE INDEX IF NOT EXISTS idx_challenges_domain_created_outcome ON vayushield_challenges(domain_id, created_at, outcome);
