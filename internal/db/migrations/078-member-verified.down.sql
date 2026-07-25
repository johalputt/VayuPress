DROP INDEX IF EXISTS idx_members_verified_at;
ALTER TABLE members DROP COLUMN verified_at;
