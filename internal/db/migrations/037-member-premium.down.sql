DROP TABLE IF EXISTS member_label_map;
DROP TABLE IF EXISTS member_labels;
DROP TABLE IF EXISTS member_subscriptions;
DROP TABLE IF EXISTS member_tiers;
-- Note: the added members columns (name, note, newsletter_opt_in, last_seen_at)
-- are intentionally retained; SQLite DROP COLUMN support is version-dependent
-- and these columns are harmless if the migration is rolled back.
