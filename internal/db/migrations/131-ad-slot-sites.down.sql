-- Down for 131: the index goes; the column stays.
--
-- SQLite drops a column only by rebuilding the table, and the column is
-- additive with a default that means "the install's own site", so an older
-- binary reads the table unchanged with it in place.
DROP INDEX IF EXISTS idx_ad_slots_site;
