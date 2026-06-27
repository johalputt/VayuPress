DROP INDEX IF EXISTS idx_articles_is_page;
DROP INDEX IF EXISTS idx_articles_featured;
-- The added articles columns (excerpt, feature_image, meta_*, canonical_url,
-- og_*, twitter_*, featured, is_page) are intentionally retained; SQLite
-- DROP COLUMN support is version-dependent and these columns are harmless if
-- the migration is rolled back.
