-- Migration 129: YouTube channels followed as blogs, and every video one has
-- made a post of, or found and held. A video's row is written before its post,
-- so two checks at once never make two posts, and is kept after a post is
-- deleted, so a deleted post is not made again. One statement per line, as the
-- runner requires.
CREATE TABLE IF NOT EXISTS youtube_channels(id TEXT PRIMARY KEY, domain_id TEXT NOT NULL DEFAULT '', channel_id TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', auto_post INTEGER NOT NULL DEFAULT 1, as_draft INTEGER NOT NULL DEFAULT 0, shorts INTEGER NOT NULL DEFAULT 1, paused INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL, last_check DATETIME, next_check DATETIME NOT NULL, last_found INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '', failures INTEGER NOT NULL DEFAULT 0, backfill_token TEXT NOT NULL DEFAULT '', backfill_done INTEGER NOT NULL DEFAULT 0, backfill_seen INTEGER NOT NULL DEFAULT 0, backfill_total INTEGER NOT NULL DEFAULT 0, UNIQUE(domain_id, channel_id));
CREATE INDEX IF NOT EXISTS idx_youtube_channels_due ON youtube_channels(paused, next_check);
CREATE TABLE IF NOT EXISTS youtube_videos(domain_id TEXT NOT NULL DEFAULT '', video_id TEXT NOT NULL, channel TEXT NOT NULL, kind TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', published_at DATETIME, start_at DATETIME, slug TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, created_at DATETIME NOT NULL, PRIMARY KEY(domain_id, video_id));
CREATE INDEX IF NOT EXISTS idx_youtube_videos_channel ON youtube_videos(channel, state);
