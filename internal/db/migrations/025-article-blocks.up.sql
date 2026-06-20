-- Admin v3 block editor (ADR-0068, Phase 3).
-- blocks_json stores the canonical block document as JSON. The rendered HTML
-- continues to live in articles.content (kept in sync on save) so every existing
-- reader, feed, and search path keeps working unchanged. An empty string means
-- the article predates the block editor and is edited as raw Markdown/HTML.
ALTER TABLE articles ADD COLUMN blocks_json TEXT NOT NULL DEFAULT '';
