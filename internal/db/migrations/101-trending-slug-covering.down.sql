-- Migration 101 (down): drop the covering index for trending's slug check.
DROP INDEX IF EXISTS idx_articles_slug_listed;
