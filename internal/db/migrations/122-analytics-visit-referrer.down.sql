-- Down for 122: nothing to undo by hand.
--
-- SQLite drops a column only by rebuilding the table, and the column is
-- additive with a default an older binary never reads.
DROP INDEX IF EXISTS idx_asession_domain_referrer;
