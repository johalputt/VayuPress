-- Multi-author: attribute each article to a staff user. Empty (the default for
-- existing posts) falls back to the site-wide author at render time, so the
-- ~234k legacy rows are unaffected until an author is assigned in the editor.
ALTER TABLE articles ADD COLUMN author_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_articles_author ON articles(author_id);
