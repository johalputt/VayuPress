-- Down for 129: no channels followed, no record of the videos made posts of.
DROP INDEX IF EXISTS idx_youtube_videos_channel;
DROP TABLE IF EXISTS youtube_videos;
DROP INDEX IF EXISTS idx_youtube_channels_due;
DROP TABLE IF EXISTS youtube_channels;
