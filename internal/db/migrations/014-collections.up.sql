CREATE TABLE IF NOT EXISTS collections(id TEXT PRIMARY KEY,title TEXT NOT NULL,slug TEXT NOT NULL UNIQUE,description TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS collection_articles(collection_id TEXT NOT NULL,article_id TEXT NOT NULL,position INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(collection_id, article_id));
CREATE INDEX IF NOT EXISTS idx_collection_articles_coll ON collection_articles(collection_id, position);
