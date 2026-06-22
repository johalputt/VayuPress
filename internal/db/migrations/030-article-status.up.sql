-- Draft/publish workflow: articles gain a status column. Existing rows default
-- to 'published' so the public site is unchanged after upgrade. Drafts are
-- excluded from every public read path (homepage, article page, feeds, sitemap,
-- search) and only visible inside VayuOS.
ALTER TABLE articles ADD COLUMN status TEXT NOT NULL DEFAULT 'published';
CREATE INDEX IF NOT EXISTS idx_articles_status ON articles(status, created_at DESC);
