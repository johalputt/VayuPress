-- Per-post publishing options: the editor's "Post settings" panel. These give
-- an operator explicit control over the metadata that, until now, was derived
-- from content + global site settings (excerpt, share image, SEO title and
-- description, canonical URL, social cards) plus two organisational flags
-- (featured, page-vs-post). The rendered article continues to fall back to the
-- derived values whenever a field is left blank, so existing posts are
-- unchanged after upgrade.
--
-- IMPORTANT: the migration runner executes ONE statement per line, so every
-- statement below MUST stay on a single line (see internal/db/db.go).
ALTER TABLE articles ADD COLUMN excerpt TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN feature_image TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN meta_title TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN meta_description TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN canonical_url TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN og_title TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN og_description TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN og_image TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN twitter_title TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN twitter_description TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN twitter_image TEXT NOT NULL DEFAULT '';
ALTER TABLE articles ADD COLUMN featured INTEGER NOT NULL DEFAULT 0;
ALTER TABLE articles ADD COLUMN is_page INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_articles_featured ON articles(featured);
CREATE INDEX IF NOT EXISTS idx_articles_is_page ON articles(is_page);
