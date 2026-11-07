-- Migration 134: the posts the card-image job has tried, so a post is tried
-- once, not on every pass. result is the media URL it was given, or why it
-- got none. post_created is the post's date: the job goes on from the
-- newest and oldest it has tried, so it never reads a post twice. One
-- statement per line, as the runner requires.
CREATE TABLE IF NOT EXISTS post_image_tries(slug TEXT PRIMARY KEY, post_created DATETIME NOT NULL, tried_at DATETIME NOT NULL, result TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_post_image_tries_created ON post_image_tries(post_created);
