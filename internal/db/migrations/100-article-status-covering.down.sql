-- Migration 100 (down): drop the covering index for tag-page counts.
DROP INDEX IF EXISTS idx_articles_id_status;
