-- Migration 094 (down): drop the sitemap covering indexes.
DROP INDEX IF EXISTS idx_articles_sitemap_domain;
DROP INDEX IF EXISTS idx_articles_sitemap;
