CREATE TABLE IF NOT EXISTS article_versions(
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  article_id TEXT    NOT NULL,
  slug       TEXT    NOT NULL,
  title      TEXT    NOT NULL,
  content    TEXT    NOT NULL,
  tags       TEXT    NOT NULL DEFAULT '',
  saved_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  label      TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_article_versions_article ON article_versions(article_id, saved_at DESC);
