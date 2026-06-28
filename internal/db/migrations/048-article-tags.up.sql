-- Normalised tag membership table (one row per article/tag pair). The articles
-- table stores tags as a single comma-separated string, so every "find posts
-- with tag X" query (per-tag page, related posts, tag index, JSON list filter)
-- was forced to run `tags LIKE '%X%'` — a predicate that cannot use an index and
-- therefore full-scans the entire articles table. On a large catalogue (hundreds
-- of thousands of posts) that scan exceeds the request timeout and surfaces as a
-- 502. This join table turns those lookups into indexed point queries whose cost
-- is bounded by how many posts carry the tag (not by the table size), so they
-- stay fast at 1M+ posts. It is kept in sync transactionally with every article
-- write plus a one-time batched backfill of existing rows.
--
-- tag        keeps the original (trimmed) display casing for the tag index counts.
-- tag_norm   is the lower-cased form used for case-insensitive membership lookups.
-- created_at is copied from the article (immutable) so the per-tag listing can be
--            served straight from the index in recency order — no full-table scan
--            and no temp-b-tree sort, for any tag however rare or popular.
--
-- The composite (tag_norm, created_at DESC, article_id) index is the workhorse:
-- it answers "newest posts with this tag" as an index range scan plus a primary-
-- key join. (article_id) supports membership/delete lookups and the backfill's
-- NOT EXISTS probe; (tag) supports the GROUP BY tag count for the topic index.
--
-- IMPORTANT: the migration runner executes ONE statement per line, so each
-- statement below must stay on a single line.
CREATE TABLE IF NOT EXISTS article_tags(article_id TEXT NOT NULL, tag TEXT NOT NULL, tag_norm TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_article_tags_norm_created ON article_tags(tag_norm, created_at DESC, article_id);
CREATE INDEX IF NOT EXISTS idx_article_tags_article ON article_tags(article_id);
CREATE INDEX IF NOT EXISTS idx_article_tags_tag ON article_tags(tag);
