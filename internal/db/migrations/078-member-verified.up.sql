-- Migration 078: a member row must mean "this person proved they control this address".
-- verified_at records the moment that proof happened, so an address that was merely
-- typed into a signup form can never be counted as a member.
ALTER TABLE members ADD COLUMN verified_at DATETIME;
-- Backfill the members whose control of the address is already evidenced: they have
-- signed in at least once (which requires a consumed magic link or a verified mailbox
-- credential), or they hold a paid tier (a completed payment carries the address).
-- Anything left NULL was created by the old pre-send upsert and was never confirmed;
-- the console surfaces those so the operator can remove them.
UPDATE members SET verified_at = created_at WHERE verified_at IS NULL AND (last_seen_at IS NOT NULL OR (tier <> 'free' AND tier <> ''));
CREATE INDEX IF NOT EXISTS idx_members_verified_at ON members(verified_at);
