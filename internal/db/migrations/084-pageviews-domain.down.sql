-- Down for 084: drop the indexes only.
--
-- SQLite cannot drop a column without rebuilding the table, and rebuilding this
-- one would mean copying every pageview row on the install to remove a field
-- that costs nothing to leave in place. The column is additive with a default,
-- so an older binary ignores it and reads exactly as it did before.
DROP INDEX IF EXISTS idx_apv_domain_created;
DROP INDEX IF EXISTS idx_apv_domain_event;
