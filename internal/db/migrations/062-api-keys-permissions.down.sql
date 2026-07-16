-- Reverse 062. SQLite cannot DROP COLUMN in older engines, so the additive
-- columns are left in place (harmless: they default to no-op values); only the
-- owner index is dropped.
DROP INDEX IF EXISTS idx_vayu_api_keys_owner;
