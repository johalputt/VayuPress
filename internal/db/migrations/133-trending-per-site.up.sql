-- Migration 133: trending keeps each site's own posts. The slug check that
-- trending answers from an index (101) now also tests the post's site, and
-- domain_id is stored after content like status, so the index carries it too;
-- without it every viewed slug would read its post's body again. It replaces
-- 101's index, whose columns it holds. One statement per line, as the runner
-- requires.
DROP INDEX IF EXISTS idx_articles_slug_listed;
CREATE INDEX IF NOT EXISTS idx_articles_slug_site_listed ON articles(slug,domain_id,status,is_page,created_at);
