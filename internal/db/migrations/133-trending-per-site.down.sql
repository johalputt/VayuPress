-- Migration 133 (down): back to 101's slug check.
CREATE INDEX IF NOT EXISTS idx_articles_slug_listed ON articles(slug,status,is_page,created_at);
DROP INDEX IF EXISTS idx_articles_slug_site_listed;
