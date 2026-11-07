-- Migration 131: which site an ad slot belongs to.
-- domain_id is '' for the install's own site, as before, or a hosted site's
-- id. Existing slots stay the install's. One statement per line, as the
-- runner requires.
ALTER TABLE ad_slots ADD COLUMN domain_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_ad_slots_site ON ad_slots(domain_id,placement,enabled);
