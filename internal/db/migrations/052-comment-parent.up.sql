-- Threaded comment replies. A non-empty parent_id points at the comment this one
-- replies to; top-level comments keep the empty-string default. Indexed so a
-- thread's replies can be gathered without scanning the table.
ALTER TABLE comments ADD COLUMN parent_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_comments_parent ON comments(parent_id);
