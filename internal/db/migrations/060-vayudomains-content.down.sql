DROP INDEX IF EXISTS idx_articles_domain_created;
DROP INDEX IF EXISTS idx_articles_domain;
ALTER TABLE articles DROP COLUMN domain_id;
