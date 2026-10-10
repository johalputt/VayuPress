-- Migration 106 (down): the triggers and the index go; the two columns are retained (SQLite DROP COLUMN support is version-dependent) and the orphaned rows removed stay removed.
DROP TRIGGER IF EXISTS article_tags_on_delete;
DROP TRIGGER IF EXISTS article_tags_on_insert;
DROP TRIGGER IF EXISTS article_tags_on_article_insert;
DROP TRIGGER IF EXISTS article_tags_on_article_update;
DROP INDEX IF EXISTS idx_article_tags_live;
