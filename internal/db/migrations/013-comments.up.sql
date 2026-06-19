CREATE TABLE IF NOT EXISTS comments(
  id         TEXT    PRIMARY KEY,
  article_id TEXT    NOT NULL,
  author     TEXT    NOT NULL,
  email      TEXT    NOT NULL DEFAULT '',
  body       TEXT    NOT NULL,
  status     TEXT    NOT NULL DEFAULT 'pending',
  ip         TEXT    NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_comments_article ON comments(article_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_comments_status  ON comments(status, created_at DESC);
